package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
)

var migrate = &command{
	name:    "migrate",
	summary: "Apply the pending database migrations",
	help: `
Applies the pending database migrations, then creates or updates the
application login.

It connects as the superuser in STORYCREEP_DATABASE_ADMIN_URL, and applies
the migrations embedded in this binary in order, each in its own
transaction. Several instances may run it at once: they take turns under an
advisory lock, and each migration is applied once. If a migration fails, the
database stays at the last one that succeeded, and the command exits with
status 1.

The application login is the one in STORYCREEP_DATABASE_URL. The command
creates it if it does not exist, sets its password to the one the server
logs in with (the URL's, or else the one PGPASSWORD, a service file, or a
password file supplies), and grants it the storycreep_app role. It also
revokes CONNECT on the database from PUBLIC and grants it to the login, so
that another role that is not a superuser connects only if it is granted
CONNECT itself. A login belongs to the whole PostgreSQL server, as does the
storycreep_app role, so each Storycreep database on a server needs a login
of its own. The command refuses a login that row-level security would not
apply to, such as a superuser, and one whose password has expired.

Last, it connects as the application login, as the server does, and fails
unless the database it reaches has every migration applied.`,
	loadsConfig: true,
	run: func(ctx context.Context, inv *invocation) error {
		res, err := database.Migrate(ctx, inv.cfg.DatabaseAdminURL, inv.cfg.DatabaseURL, database.MigrateOptions{})
		for _, m := range res.Applied {
			fmt.Fprintf(inv.stdout, "Applied %s in %s.\n", m.Name, m.Duration.Round(time.Millisecond))
		}
		if err != nil {
			return err
		}
		if len(res.Applied) == 0 {
			fmt.Fprint(inv.stdout, "No migrations to apply. ")
		}
		fmt.Fprintf(inv.stdout, "The database is at version %d, and the application login %q is ready.\n",
			res.Version, inv.cfg.DatabaseURL.User())
		return nil
	},
}
