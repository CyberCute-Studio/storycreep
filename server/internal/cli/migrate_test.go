package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
)

func migrateEnv(d *dbtest.Database, admin bool) map[string]string {
	env := map[string]string{"STORYCREEP_PUBLIC_URL": testURL, "STORYCREEP_DATABASE_URL": d.AppURL.Reveal()}
	if admin {
		env["STORYCREEP_DATABASE_ADMIN_URL"] = d.AdminURL.Reveal()
	}
	return env
}

func TestMigrate(t *testing.T) {
	t.Parallel()
	d := dbtest.Empty(t)

	code, stdout, stderr := run(t, migrateEnv(d, true), "migrate")
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	want := regexp.MustCompile(`^Applied 00001_roles\.sql in \S+\.\n(?:Applied .*\n)*` +
		`The database is at version \d+, and the application login "` + dbtest.AppLogin + `" is ready\.\n$`)
	if !want.MatchString(stdout) {
		t.Errorf("stdout:\n%s", stdout)
	}

	code, stdout, _ = run(t, migrateEnv(d, true), "migrate")
	if code != ExitOK || !strings.HasPrefix(stdout, "No migrations to apply. The database is at version ") {
		t.Errorf("the second run: exit %d, stdout %q", code, stdout)
	}
}

// TestMigrateFailures checks that migrate exits with status 1, saying why,
// when it cannot apply the migrations.
//
// Verifies: FR-ADMIN-06/AC1
func TestMigrateFailures(t *testing.T) {
	t.Parallel()
	d := dbtest.Empty(t)

	code, stdout, stderr := run(t, migrateEnv(d, false), "migrate")
	if code != ExitFailure || stdout != "" || !strings.Contains(stderr, "STORYCREEP_DATABASE_ADMIN_URL is not set") {
		t.Errorf("without the admin URL: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	env := migrateEnv(d, true)
	env["STORYCREEP_DATABASE_URL"] = d.AdminURL.Reveal()
	env["STORYCREEP_DATABASE_ADMIN_URL"] = d.NewLogin(t, "SUPERUSER").Reveal()
	code, _, stderr = run(t, env, "migrate")
	if code != ExitFailure || !strings.Contains(stderr, `storycreep migrate: the login "postgres" in STORYCREEP_DATABASE_URL`) ||
		strings.Contains(stderr, d.AdminURL.Password().Reveal()) {
		t.Errorf("with a superuser as the application login: exit %d, stderr %q", code, stderr)
	}
}
