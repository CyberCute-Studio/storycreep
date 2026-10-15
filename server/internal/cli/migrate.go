package cli

import (
	"context"
	"fmt"

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
creates it if it does not exist, sets its password to the URL's, and grants
it the storycreep_app role. It refuses a login that row-level security would
not apply to, such as a superuser.`,
	loadsConfig: true,
	run: func(ctx context.Context, inv *invocation) error {
		res, err := database.Migrate(ctx, inv.cfg.DatabaseAdminURL, inv.cfg.DatabaseURL, database.MigrateOptions{})
		for _, m := range res.Applied {
			fmt.Fprintf(inv.stdout, "Applied %s in %s.\n", m.Name, m.Duration.Round(1e6))
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
