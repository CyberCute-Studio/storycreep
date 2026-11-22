package authoring

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }

// TestEveryProjectTableIsAccounted checks that every table that holds a
// project's rows is either in a section of the format or in NotExported, with
// why, so that a new table of authoring data cannot be left out of the
// archive unnoticed (standing checklist), and that neither list names a table
// that does not exist.
func TestEveryProjectTableIsAccounted(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	rows, err := d.Admin(t).Query(context.Background(), `SELECT c.table_name FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND c.column_name = 'project_id' AND t.table_type = 'BASE TABLE'
		UNION SELECT 'project' UNION SELECT 'snapshot_file'`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	accounted := map[string]bool{}
	for _, s := range Sections() {
		for _, table := range s.Tables {
			accounted[table] = true
		}
	}
	for table, why := range NotExported {
		if why == "" {
			t.Errorf("NotExported gives no reason for %s", table)
		}
		accounted[table] = true
	}
	for _, table := range tables {
		if !accounted[table] {
			t.Errorf("the table %s holds a project's rows, and is neither in a section nor in NotExported", table)
		}
	}
	for table := range accounted {
		if !slices.Contains(tables, table) {
			t.Errorf("the registry names %s, which is not a table of a project's rows", table)
		}
	}
}
