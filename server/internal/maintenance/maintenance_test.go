package maintenance

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestMain runs the tests with dbtest.Run, which keeps the libpq environment
// variables and the user's password file out of them.
func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

func open(t *testing.T, url config.DatabaseURL) *DB {
	t.Helper()
	db, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// TestOnlyMaintenanceBypassesRowLevelSecurity checks that after migrating,
// storycreep_maintenance is the only role with BYPASSRLS, apart from the
// superuser that applies the migrations (PRD §5.4). It reads every role of
// the test server, so it does not run in parallel with the tests that create
// roles.
func TestOnlyMaintenanceBypassesRowLevelSecurity(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	rows, err := d.Admin(t).Query(ctx, "SELECT rolname FROM pg_roles WHERE rolbypassrls OR rolsuper ORDER BY rolname")
	var got []string
	if err == nil {
		got, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{d.AdminURL.User(), database.MaintenanceRole}; !slices.Equal(got, want) {
		t.Errorf("the roles that bypass row-level security are %v, want %v", got, want)
	}
}

// TestInTxBypassesRowLevelSecurity checks that the maintenance path's
// transactions act as storycreep_maintenance for the maintenance login, and
// see the rows of every workspace, where the application login's see only
// their own workspace's.
func TestInTxBypassesRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	f := isolationtest.New(t, d, isolationtest.Tables)
	db := open(t, d.MaintenanceURL)

	memberships := func(tx pgx.Tx) []ids.UUID {
		t.Helper()
		rows, err := tx.Query(ctx, "SELECT id FROM membership ORDER BY id")
		var got []ids.UUID
		if err == nil {
			got, err = pgx.CollectRows(rows, pgx.RowTo[ids.UUID])
		}
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	both := []ids.UUID{f.A.Rows["membership"], f.B.Rows["membership"]}
	slices.SortFunc(both, func(a, b ids.UUID) int { return strings.Compare(a.String(), b.String()) })
	err := db.inTx(ctx, func(tx pgx.Tx) error {
		var session, current string
		if err := tx.QueryRow(ctx, "SELECT session_user, current_user").Scan(&session, &current); err != nil {
			return err
		}
		if session != dbtest.MaintenanceLogin || current != database.MaintenanceRole {
			t.Errorf("the transaction's session user is %s and current user %s, want %s and %s",
				session, current, dbtest.MaintenanceLogin, database.MaintenanceRole)
		}
		if got := memberships(tx); !slices.Equal(got, both) {
			t.Errorf("the maintenance path sees the memberships %v, want both workspaces' %v", got, both)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = f.DB.InTx(ctx, f.A.Scope(), func(tx pgx.Tx) error {
		if got := memberships(tx); !slices.Equal(got, []ids.UUID{f.A.Rows["membership"]}) {
			t.Errorf("the application login sees the memberships %v, want only workspace A's", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestOpenRefusesOtherLogins checks that Open refuses a login that is not the
// maintenance login that migrating provisions: one that has more privileges
// than storycreep_maintenance gives it, or that cannot act as that role.
func TestOpenRefusesOtherLogins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)

	if _, err := Open(ctx, config.DatabaseURL{}); !errors.Is(err, ErrNoURL) {
		t.Errorf("Open without a URL returned %v, want ErrNoURL", err)
	}

	stranger := d.NewLogin(t, "")
	withBypass := d.NewLogin(t, "")
	bypass := pgx.Identifier{withBypass.User()}.Sanitize()
	if _, err := admin.Exec(ctx, "GRANT storycreep_maintenance TO "+bypass+"; ALTER ROLE "+bypass+" BYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		url    config.DatabaseURL
		unsafe bool
		want   string
	}{
		{"the application login", d.AppURL, true,
			`it is a member of "storycreep_app", which is reserved for the server's other work`},
		{"the superuser", d.AdminURL, true, "it is a superuser"},
		{"a login with BYPASSRLS", withBypass, true, "it has BYPASSRLS"},
		{"a login without the role", stranger, false, "cannot act as storycreep_maintenance"},
	} {
		_, err := Open(ctx, tt.url)
		var uerr *database.UnsafeLoginError
		if err == nil || errors.As(err, &uerr) != tt.unsafe || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Open returned %v, want an error saying %q", tt.name, err, tt.want)
			continue
		}
		if tt.unsafe && (uerr.Key != "STORYCREEP_DATABASE_MAINTENANCE_URL" ||
			!strings.Contains(err.Error(), "must be an ordinary role that bypasses row-level security only by "+
				"acting as storycreep_maintenance")) {
			t.Errorf("%s: Open returned %v, which does not name the maintenance login's key and rule", tt.name, err)
		}
	}
}
