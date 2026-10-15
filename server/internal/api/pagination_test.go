package api

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// itemRow is a row of the test table, and itemKey its sort key.
type itemRow struct {
	Created time.Time
	ID      string
}

type itemKey struct {
	Created time.Time `json:"c"`
	ID      string    `json:"i"`
}

func (r itemRow) key() itemKey {
	return itemKey(r)
}

func (k itemKey) less(o itemKey) bool {
	return k.Created.Before(o.Created) || k.Created.Equal(o.Created) && k.ID < o.ID
}

// TestKeysetPagination pages through a table, ordered by (created_at, id),
// while other sessions add rows between the pages: before the page just
// read, between its rows, and after it. A list paged by OFFSET would repeat
// a row each time one is added before the page, and skip one each time a row
// is removed. Keyset pagination returns every row that was there throughout
// exactly once, in order, whatever is added meanwhile.
//
// Verifies: FR-API-04/AC2
func TestKeysetPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	conn := dbtest.New(t).Admin(t)
	if _, err := conn.Exec(ctx, "CREATE TABLE items (created_at timestamptz NOT NULL, id uuid PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	insert := func(created time.Time) {
		t.Helper()
		if _, err := conn.Exec(ctx, "INSERT INTO items VALUES ($1, $2)", created, ids.New().String()); err != nil {
			t.Fatal(err)
		}
	}
	// Rows share timestamps in threes, so that the id breaks the ties.
	for i := range 30 {
		insert(base.Add(time.Duration(i/3) * time.Minute))
	}
	initial := readAll(t, conn)

	list := func(p PageParams) Page[itemRow] {
		t.Helper()
		var rows pgx.Rows
		var err error
		if p.Cursor == "" {
			rows, err = conn.Query(ctx, "SELECT created_at, id::text FROM items ORDER BY created_at, id LIMIT $1", p.Fetch())
		} else {
			var k itemKey
			if k, err = DecodeCursor[itemKey](p.Cursor); err != nil {
				t.Fatal(err)
			}
			rows, err = conn.Query(ctx, `SELECT created_at, id::text FROM items
				WHERE (created_at, id) > ($1, $2::uuid) ORDER BY created_at, id LIMIT $3`, k.Created, k.ID, p.Fetch())
		}
		if err != nil {
			t.Fatal(err)
		}
		items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[itemRow])
		if err != nil {
			t.Fatal(err)
		}
		page, err := NewPage(p, items, itemRow.key)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}

	var seen []itemRow
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 50 {
			t.Fatal("the pages do not end")
		}
		page := list(PageParams{Cursor: cursor, Limit: 4})
		seen = append(seen, page.Items...)
		if len(page.Items) > 0 {
			last := page.Items[len(page.Items)-1].Created
			insert(base.Add(-time.Hour))                         // before every page
			insert(last)                                         // among the page's rows, or just after them
			insert(last.Add(30 * time.Second))                   // after the page
			insert(base.Add(time.Duration(pages) * time.Second)) // somewhere already read
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}

	for i := 1; i < len(seen); i++ {
		if !seen[i-1].key().less(seen[i].key()) {
			t.Fatalf("row %d %v does not sort after row %d %v: a row is repeated or out of order", i, seen[i], i-1,
				seen[i-1])
		}
	}
	count := map[string]int{}
	for _, r := range seen {
		count[r.ID]++
	}
	for _, r := range initial {
		if count[r.ID] != 1 {
			t.Errorf("the row %v was read %d times, want once", r, count[r.ID])
		}
	}
}

// readAll returns every row of the test table.
func readAll(t *testing.T, conn *pgx.Conn) []itemRow {
	t.Helper()
	rows, err := conn.Query(context.Background(), "SELECT created_at, id::text FROM items ORDER BY created_at, id")
	if err != nil {
		t.Fatal(err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[itemRow])
	if err != nil {
		t.Fatal(err)
	}
	return items
}
