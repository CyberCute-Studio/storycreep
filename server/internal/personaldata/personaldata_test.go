package personaldata

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables and the user's password file out of them.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// prdInventory returns the first column of each row of the inventory of PRD
// §9.6, in its order.
func prdInventory(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "docs", "PRD.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	var rows []string
	in, table := false, false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "### 9.6 Privacy and data inventory":
			in = true
		case !in:
		case strings.HasPrefix(line, "| Data | Fields | In export |"):
			table = true
		case table && strings.HasPrefix(line, "| ---"):
		case table && strings.HasPrefix(line, "| "):
			rows = append(rows, strings.TrimSpace(strings.Split(line, "|")[1]))
		case table:
			return rows
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("the PRD has no inventory in §9.6")
	return nil
}

// TestInventoryIsThePRDs checks that the inventory has a row of each row of
// the PRD's, in its order, so that the archive holds every row that the PRD
// lists, and each row is held in a file of the archive.
//
// Verifies: FR-GDPR-01/AC2
func TestInventoryIsThePRDs(t *testing.T) {
	t.Parallel()
	var got []string
	for _, r := range Inventory {
		got = append(got, r.Data)
		if len(r.Paths) == 0 {
			t.Errorf("the row %q is held in no file of the archive", r.Data)
		}
	}
	if want := prdInventory(t); !slices.Equal(got, want) {
		t.Errorf("the inventory's rows are\n%q\nwant the PRD's\n%q", got, want)
	}
}

// TestEveryActionIsLabelled checks that every action of the audit log has the
// labels of an actor other than the user, so that an entry about the user
// never names who acted on them (PRD §9.6), and that the labels are of
// actions that the registry has.
//
// Verifies: FR-GDPR-01/AC4
func TestEveryActionIsLabelled(t *testing.T) {
	t.Parallel()
	actions := audit.Actions()
	for _, a := range actions {
		if _, ok := actorLabels[a]; !ok {
			t.Errorf("%s has no labels of its actor; add them to actorLabels", a)
		}
	}
	for a, l := range actorLabels {
		if !slices.Contains(actions, a) {
			t.Errorf("%s has labels, but the audit log does not register it", a)
		}
		for _, label := range []string{l.other, l.none} {
			if label != "" && label != LabelWorkspaceAdmin && label != LabelInstanceAdmin {
				t.Errorf("%s has the label %q, which is no role's", a, label)
			}
		}
	}
	if got := ActorLabel(string(audit.ActionMembershipRemove), true); got != LabelWorkspaceAdmin {
		t.Errorf("a removal by someone else is labelled %q, want %q", got, LabelWorkspaceAdmin)
	}
	if got := ActorLabel(string(audit.ActionLogin), false); got != "" {
		t.Errorf("a failed sign-in is labelled %q, want none", got)
	}
}

// TestRegistries checks the registries alone: each reference either names a
// file or says why it has none, no column is listed twice, and each
// attribution's relation is one that the format has.
func TestRegistries(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, r := range References {
		key := r.Table + "." + r.Column
		if seen[key] {
			t.Errorf("%s is listed twice", key)
		}
		seen[key] = true
		if (r.Path == "") == (r.Why == "") {
			t.Errorf("%s names a file and why it has none, or neither", key)
		}
	}
	for _, a := range Attributions {
		key := a.Table + "." + a.Column
		if seen[key] {
			t.Errorf("%s is listed twice", key)
		}
		seen[key] = true
		if !slices.Contains(Relations, a.Relation) {
			t.Errorf("%s has the relation %q, which the format does not", key, a.Relation)
		}
	}
	held := map[string]bool{}
	for _, c := range AccountColumns {
		if held[c.Column] {
			t.Errorf("the account's %s is listed twice", c.Column)
		}
		held[c.Column] = true
		if c.Held == (c.Why != "") {
			t.Errorf("the account's %s is held and says why not, or neither", c.Column)
		}
	}
}

// TestEveryUserReferenceIsAccounted checks that every foreign key to "user" in
// the schema is one of References or Attributions, and each of those one of
// them, so that a table that comes to refer to an account says whether the
// personal-data export holds its rows, and that each attribution's columns of
// when and of the project are its table's.
//
// Verifies: FR-GDPR-01/AC2
func TestEveryUserReferenceIsAccounted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	conn := dbtest.New(t).Admin(t)
	rows, err := conn.Query(ctx, `SELECT c.conrelid::regclass::text || '.' || a.attname
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f' AND c.confrelid = 'public."user"'::regclass AND cardinality(c.conkey) = 1
		ORDER BY 1`)
	var got []string
	if err == nil {
		got, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, r := range References {
		listed = append(listed, r.Table+"."+r.Column)
	}
	for _, a := range Attributions {
		listed = append(listed, a.Table+"."+a.Column)
	}
	for _, c := range got {
		if !slices.Contains(listed, c) {
			t.Errorf("%s refers to an account, but is neither a reference nor an attribution of the "+
				"personal-data export; add it to References, with its file or why it has none", c)
		}
	}
	for _, c := range listed {
		if !slices.Contains(got, c) {
			t.Errorf("%s is listed, but is no foreign key to \"user\"", c)
		}
	}
	for _, a := range Attributions {
		for _, column := range []string{a.At, a.Project, "workspace_id", "id"} {
			var n int
			if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, a.Table, column).
				Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Errorf("%s has no column %s, which its attribution reads", a.Table, column)
			}
		}
	}
}

// TestEveryAccountColumnIsAccounted checks that every column of "user" is one
// of AccountColumns, held or with why it is not, and each of those a column,
// so that a column that comes to hold more of an account, as a credential's,
// says what the export holds of it.
//
// Verifies: FR-GDPR-01/AC2, FR-GDPR-01/AC3
func TestEveryAccountColumnIsAccounted(t *testing.T) {
	t.Parallel()
	rows, err := dbtest.New(t).Admin(t).Query(context.Background(), `SELECT column_name::text
		FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'user' ORDER BY 1`)
	var got []string
	if err == nil {
		got, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, c := range AccountColumns {
		listed = append(listed, c.Column)
	}
	slices.Sort(listed)
	if !slices.Equal(got, listed) {
		t.Errorf("the columns of \"user\" are\n%q\nand AccountColumns lists\n%q", got, listed)
	}
}
