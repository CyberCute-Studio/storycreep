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
Runs the server: the API and the health endpoints on STORYCREEP_HTTP_ADDR,
and /metrics on the admin port, STORYCREEP_ADMIN_ADDR. It logs to standard
error, one JSON object per line. A warning about the configuration, and the
error that stops the server, are written there as lines of plain text too,
starting with "storycreep serve:".

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

It stops on SIGINT or SIGTERM: it stops accepting connections, waits up to
20 seconds for the requests in progress, and exits with status 0. A second
signal ends it at once.`,
	loadsConfig: true,
	flags: func(fs *flag.FlagSet) {
		fs.Bool("no-auto-migrate", false, "do not apply pending migrations at startup; 'storycreep migrate' applies them")
	},
	run: func(ctx context.Context, inv *invocation) error {
		return httpserver.Run(ctx, inv.cfg, httpserver.Options{
			AutoMigrate: !inv.boolFlag("no-auto-migrate"),
			Log:         logging.New(inv.stderr, inv.cfg.LogLevel),
		})
	},
}
