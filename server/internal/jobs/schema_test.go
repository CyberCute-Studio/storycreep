package jobs

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

// riverTables selects River's tables in the public schema, as c, but for
// river_migration, which River's own migrator records its migrations in, and
// which the server's migrations leave out.
const riverTables = `c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p')
	AND c.relname LIKE 'river\_%' AND c.relname <> 'river_migration'`

// riverSchema describes River's objects in the database of conn, one line
// each, in order: its tables, columns, constraints, indexes, triggers,
// sequences, functions, and types.
func riverSchema(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		SELECT 'table ' || c.relname || ' persistence ' || c.relpersistence::text
		FROM pg_class c WHERE `+riverTables+`
		UNION ALL
		SELECT 'column ' || c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
			|| CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE '' END
			|| coalesce(' DEFAULT ' || pg_get_expr(ad.adbin, ad.adrelid), '')
		FROM pg_class c
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
			LEFT JOIN pg_attrdef ad ON ad.adrelid = c.oid AND ad.adnum = a.attnum
		WHERE `+riverTables+`
		UNION ALL
		SELECT 'constraint ' || c.relname || ' ' || con.conname || ' ' || pg_get_constraintdef(con.oid)
		FROM pg_class c JOIN pg_constraint con ON con.conrelid = c.oid WHERE `+riverTables+`
		UNION ALL
		SELECT 'index ' || pg_get_indexdef(i.indexrelid)
		FROM pg_class c JOIN pg_index i ON i.indrelid = c.oid WHERE `+riverTables+`
		UNION ALL
		SELECT 'trigger ' || pg_get_triggerdef(tg.oid)
		FROM pg_class c JOIN pg_trigger tg ON tg.tgrelid = c.oid AND NOT tg.tgisinternal WHERE `+riverTables+`
		UNION ALL
		SELECT 'sequence ' || c.relname
		FROM pg_class c
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'S' AND c.relname LIKE 'river\_%'
			AND c.relname NOT LIKE 'river\_migration%'
		UNION ALL
		SELECT 'function ' || pg_get_functiondef(p.oid)
		FROM pg_proc p WHERE p.pronamespace = 'public'::regnamespace AND p.proname LIKE 'river\_%'
		UNION ALL
		SELECT 'type ' || ty.typname || ' ' || array_agg(e.enumlabel ORDER BY e.enumsortorder)::text
		FROM pg_type ty JOIN pg_enum e ON e.enumtypid = ty.oid
		WHERE ty.typnamespace = 'public'::regnamespace AND ty.typname LIKE 'river\_%'
		GROUP BY ty.typname
		ORDER BY 1`)
	var lines []string
	if err == nil {
		lines, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// TestRiverSchema checks that the server's migrations build River's schema as
// River's own migrator builds it, for the version of River that the server
// runs. When a newer River adds a migration, this fails until the migration is
// copied into a new migration of the server's.
func TestRiverSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ours := dbtest.New(t)
	rivers := dbtest.Empty(t)
	pool, err := pgxpool.New(ctx, rivers.AdminURL.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}

	got, want := riverSchema(t, ours.Admin(t)), riverSchema(t, rivers.Admin(t))
	if len(want) == 0 {
		t.Fatal("River's migrator built no schema")
	}
	for _, line := range want {
		if !slices.Contains(got, line) {
			t.Errorf("the migrations lack River's %s", line)
		}
	}
	for _, line := range got {
		if !slices.Contains(want, line) {
			t.Errorf("the migrations have %s, which River's do not", line)
		}
	}
}

// TestRiverPrivileges checks that the application login, which River runs
// as, can read and write each of River's tables and use its sequences, and
// that the check finds a table that it cannot.
func TestRiverPrivileges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	missing := func() []string {
		t.Helper()
		rows, err := admin.Query(ctx, `
			SELECT c.relname FROM pg_class c
			WHERE c.relnamespace = 'public'::regnamespace AND c.relname LIKE 'river\_%' AND CASE c.relkind
				WHEN 'S' THEN NOT has_sequence_privilege($1, c.oid, 'USAGE')
				ELSE c.relkind IN ('r', 'p') AND NOT (has_table_privilege($1, c.oid, 'SELECT')
					AND has_table_privilege($1, c.oid, 'INSERT') AND has_table_privilege($1, c.oid, 'UPDATE')
					AND has_table_privilege($1, c.oid, 'DELETE'))
				END
			ORDER BY 1`, dbtest.AppLogin)
		var tables []string
		if err == nil {
			tables, err = pgx.CollectRows(rows, pgx.RowTo[string])
		}
		if err != nil {
			t.Fatal(err)
		}
		return tables
	}
	if tables := missing(); len(tables) > 0 {
		t.Errorf("the application login lacks privileges on %v", tables)
	}
	if _, err := admin.Exec(ctx, "CREATE TABLE river_extra (id bigserial PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if got, want := missing(), []string{"river_extra", "river_extra_id_seq"}; !slices.Equal(got, want) {
		t.Errorf("the check finds %v, want %v", got, want)
	}
}
