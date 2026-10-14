package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
)

var validateConfig = &command{
	name:    "validate-config",
	summary: "Check the configuration and print the effective values",
	help: `
Checks the configuration without starting anything: reads the environment and
the configuration file, and validates every key.

If the configuration is valid, it prints each key's effective value and where
it came from, with secrets redacted, followed by the capabilities the
configuration resolves to. If it is invalid, it lists every problem, each
naming its key, and exits with status 1.`,
	loadsConfig: true,
	run: func(ctx context.Context, inv *invocation) error {
		w := tabwriter.NewWriter(inv.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "The configuration is valid.")
		if f := inv.cfg.File(); f != "" {
			fmt.Fprintf(w, "Configuration file: %s\n", f)
		}
		fmt.Fprint(w, "\nKEY\tVALUE\tSOURCE\n")
		for _, e := range inv.cfg.Entries() {
			fmt.Fprintf(w, "%s\t%s\t%s\n", e.Key, e.Value, e.Source)
		}
		fmt.Fprint(w, "\nCAPABILITY\tVALUE\n")
		for _, c := range inv.cfg.Capabilities.Describe() {
			fmt.Fprintf(w, "%s\t%s\n", c.Name, c.Value)
		}
		return w.Flush()
	},
}
