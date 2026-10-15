package cli

import (
	"context"
	"flag"

	"github.com/CyberCute-Studio/storycreep/server/internal/httpserver"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

var serve = &command{
	name:    "serve",
	summary: "Run the server",
	help: `
Runs the server: the API, the health endpoints, and the pages of the web
interface on STORYCREEP_HTTP_ADDR, and /metrics on the admin port,
STORYCREEP_ADMIN_ADDR. It logs to standard error, one JSON object per line.
A warning about the configuration, and the error that stops the server, are
written there as lines of plain text too, starting with "storycreep serve:".

It listens first, and then applies the pending database migrations as the
superuser in STORYCREEP_DATABASE_ADMIN_URL, under the same advisory lock as
'storycreep migrate', so that instances that start together apply each
migration once. Meanwhile, /healthz answers 200, while /readyz and the API
answer 503. If a migration fails, the database stays at the last one that
succeeded, and the server stops listening and exits with status 1.

With --no-auto-migrate, it applies none, and needs no admin URL: it serves
once the database has every migration applied, which 'storycreep migrate'
does, and checks for that every 5 seconds. Until then, /readyz and the API
answer 503.

Once it serves, it also runs the background jobs that it and the other
instances enqueue, 4 at a time, on a pool of 7 database connections of its
own.
With --no-inline-workers, it only enqueues them, for the instances started
without the flag to run, as a deployment does that runs its job workers on
instances of their own. If no instance runs them, the jobs wait.

It stops on SIGINT or SIGTERM: it stops accepting connections and taking
jobs, waits up to 20 seconds for the requests and the jobs in progress, and
exits with status 0. A job that has not finished by then is cancelled, and is
attempted again later; the server waits up to 5 seconds more for it to
return. A second signal ends it at once.`,
	loadsConfig: true,
	flags: func(fs *flag.FlagSet) {
		fs.Bool("no-auto-migrate", false, "do not apply pending migrations at startup; 'storycreep migrate' applies them")
		fs.Bool("no-inline-workers", false, "do not run background jobs in this process; instances without the flag run them")
	},
	run: func(ctx context.Context, inv *invocation) error {
		return httpserver.Run(ctx, inv.cfg, httpserver.Options{
			AutoMigrate:   !inv.boolFlag("no-auto-migrate"),
			InlineWorkers: !inv.boolFlag("no-inline-workers"),
			Log:           logging.New(inv.stderr, inv.cfg.LogLevel),
		})
	},
}
