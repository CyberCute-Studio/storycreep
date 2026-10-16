package cli

import (
	"context"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/setup"
)

var validateConfig = &command{
	name:    "validate-config",
	summary: "Check the configuration and print the effective values",
	help: `
Checks the configuration without starting anything: reads the environment and
the configuration file, and validates every key. The values of the
STORYCREEP_SETUP_ keys are checked as unattended first-run setup checks
them, except for what only the database can tell: whether the owner's
address has an account already, or another workspace has the slug.

If the configuration is valid, it prints each key's effective value and where
it came from, with secrets redacted, followed by the capabilities the
configuration resolves to. If it is invalid, it lists every problem, each
naming its key, and exits with status 1.`,
	loadsConfig: true,
	run: func(ctx context.Context, inv *invocation) error {
		if err := checkUnattendedSetup(inv.cfg); err != nil {
			return err
		}
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

// checkUnattendedSetup checks the values of the configuration's
// STORYCREEP_SETUP_ keys, which package config only checks are set, as
// unattended first-run setup checks them (PRD FR-ADMIN-02), so that a value
// that would stop the server is found here too. It returns what setup
// refuses as a *config.Error, which the frame reports as it does an invalid
// configuration, each problem with where its value came from.
func checkUnattendedSetup(cfg *config.Config) error {
	if cfg.Setup == nil {
		return nil
	}
	err := setup.CheckUnattended(cfg.Setup, cfg.DefaultLocale)
	var serr *setup.ConfigError
	if !errors.As(err, &serr) {
		return err
	}
	sources := map[string]string{}
	for _, e := range cfg.Entries() {
		sources[e.Key] = e.Source
	}
	problems := make([]config.Problem, len(serr.Problems))
	for i, p := range serr.Problems {
		problems[i] = config.Problem{Key: p.Key, Source: sources[p.Key], Msg: p.Msg}
	}
	return &config.Error{Problems: problems}
}
