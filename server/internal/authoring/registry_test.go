package authoring

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// sha256Hex returns the SHA-256 of data, in hexadecimal.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestEverySectionHasBothDirections checks that every section of the registry
// is read by the export and stored by the import, so that a table of authoring
// data cannot be registered in one direction alone (standing checklist), and
// that the tables that the import's writer fills are the registry's: a table
// that a section names and no writer fills, or the reverse, fails here.
//
// Verifies: FR-DATA-01/AC1
func TestEverySectionHasBothDirections(t *testing.T) {
	t.Parallel()
	registered := map[string]bool{}
	for _, s := range Sections() {
		if s.read == nil {
			t.Errorf("the section %s has no export (read)", s.Name)
		}
		if s.store == nil {
			t.Errorf("the section %s has no import (store)", s.Name)
		}
		for _, table := range s.Tables {
			registered[table] = true
		}
	}
	for table := range importedTables {
		if !registered[table] {
			t.Errorf("the import writes %s, which no section of the registry exports", table)
		}
	}
	for table := range registered {
		if !importedTables[table] {
			t.Errorf("the section of %s is exported, and the import writes none of it", table)
		}
	}
}

// TestDeclaredSectionRefused checks that a section that this build declares
// and no importer handles, as a test build's may, refuses an archive that holds
// it and names it, as an unknown section does (FR-DATA-01 AC12, which item 127
// runs against such a section once annotations import).
//
// Verifies: FR-DATA-01/AC12
func TestDeclaredSectionRefused(t *testing.T) {
	const name = "test_only"
	file := `{"items":[{"x":1}]}`
	for _, declared := range []bool{true, false} {
		if declared {
			declaredSections = []Section{{Name: name, Path: name + ".json"}}
		}
		entry := ManifestSection{Name: name, Path: name + ".json", Items: 1, SHA256: sha256Hex([]byte(file))}
		data := zipped(t, [2]string{ManifestPath, manifestOf(t, entry)}, [2]string{name + ".json", file})
		a, err := openArchive(bytes.NewReader(data), int64(len(data)), 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		_, err = readArchived(a)
		if declared {
			declaredSections = nil
		}
		var refused *ImportError
		if !errors.As(err, &refused) || refused.Code != FailSectionUnsupported || refused.Section != name {
			t.Errorf("declared %v: %v, want section_unsupported naming %s", declared, err, name)
		}
	}
}
