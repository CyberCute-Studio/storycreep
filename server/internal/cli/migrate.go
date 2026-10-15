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
advisory lock, and each migration is applied once. One that has to wait for
the lock says so. If a migration fails, the database stays at the last one
that succeeded, and the command exits with status 1. The command refuses a
database server older than PostgreSQL 15, and an admin login that is, or
acts as, a role the migrations create (storycreep_app, storycreep_definer,
or storycreep_maintenance), which they make unable to log in. It also
refuses a database that has applied migrations on a database server that
lacks those roles, as after a dump of the database, which holds no roles,
is restored into another server, and says how to recover.

The application login is the one in STORYCREEP_DATABASE_URL. The command
creates it if it does not exist, sets its password to the one the server
logs in with (the URL's, or else the one PGPASSWORD, a service file, or a
password file supplies), and grants it the storycreep_app role. It also
revokes CONNECT on the database from PUBLIC and from storycreep_app, and
grants it to the login, and to the admin login if that took its CONNECT,
so that no other role connects unless it is a superuser, or it is, or
inherits the privileges of, the login, the database's owner, or a role
granted CONNECT. It fails if PUBLIC or storycreep_app can still connect
afterwards, as through a grant that a role other than the database's owner
made. A login belongs to the whole PostgreSQL server, as does the
storycreep_app role, so each Storycreep database on a server needs a login
of its own. The command refuses a login that row-level security would not
apply to, such as a superuser or the database's owner, one named after a
role the migrations create or starting with pg_, which PostgreSQL reserves,
and, when the server logs in with a password, one whose VALID UNTIL has
passed.

Last, it connects as the application login, as the server does, and fails
unless the database it reaches has every migration applied.`,
	loadsConfig: true,
	run: func(ctx context.Context, inv *invocation) error {
		res, err := database.Migrate(ctx, inv.cfg.DatabaseAdminURL, inv.cfg.DatabaseURL, database.MigrateOptions{
			Waiting: func() {
				fmt.Fprintln(inv.stdout, "Waiting for the migration lock, which another session holds, as an instance "+
					"does while it migrates.")
			},
		})
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
