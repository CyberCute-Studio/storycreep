// Package cli is the storycreep command line: one binary with a subcommand per
// operation (PRD FR-ADMIN-06).
//
// Every subcommand has --help, and the exit status says how it ended: ExitOK,
// ExitFailure, or ExitUsage. A subcommand that needs the configuration
// declares it, and the frame loads and validates the configuration before the
// subcommand runs, so an invalid configuration stops every such subcommand the
// same way: with ExitFailure and a message naming each offending key
// (FR-ADMIN-01 AC1).
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
)

// The exit statuses.
const (
	// ExitOK means the command succeeded.
	ExitOK = 0
	// ExitFailure means the command failed, including because the
	// configuration is invalid.
	ExitFailure = 1
	// ExitUsage means the command line is wrong: an unknown command or flag,
	// or a missing or extra argument.
	ExitUsage = 2
)

// command is one subcommand.
type command struct {
	name string
	// summary is one line for the command list.
	summary string
	// args is the synopsis of the positional arguments. A command without
	// one takes no arguments, and the frame rejects any.
	args string
	// help is the description shown by --help.
	help string
	// loadsConfig makes the frame add --config, load the configuration, and
	// refuse to run the command if the configuration is invalid.
	loadsConfig bool
	// flags registers the command's own flags, if it has any.
	flags func(fs *flag.FlagSet)
	// run runs the command with its positional arguments. A returned error
	// ends it with ExitFailure, or ExitUsage if it is a usageError.
	run func(ctx context.Context, inv *invocation) error
}

// invocation is what a command runs with.
type invocation struct {
	args           []string
	stdin          io.Reader
	stdout, stderr io.Writer
	// flags are the parsed flags of the command.
	flags *flag.FlagSet
	// cfg is the validated configuration, for commands that load it.
	cfg *config.Config
}

// usageError is an error in how the command was invoked.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// commands are the subcommands, in the order of the command list.
var commands = []*command{
	serve,
	migrate,
	createUser,
	grantRole,
	resetPassword,
	validateConfig,
	healthcheck,
}

func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// Run runs the command line args, without the program name, with standard
// input, output, and error, and returns the exit status. env is the
// environment the configuration is read from, as config.Options.Env
// describes; the zero value is the process environment.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, env config.Environment) int {
	if len(args) == 0 {
		printUsage(stderr)
		return ExitUsage
	}
	switch name := args[0]; {
	case isHelpFlag(name):
		printUsage(stdout)
		return ExitOK
	case name == "help":
		return help(args[1:], stdout, stderr)
	default:
		c := lookup(name)
		if c == nil {
			if strings.HasPrefix(name, "-") {
				return usageFailure(stderr, "", fmt.Sprintf("unknown flag %s; flags go after the command", name))
			}
			return usageFailure(stderr, "", fmt.Sprintf("unknown command %q", name))
		}
		return c.execute(ctx, args[1:], stdin, stdout, stderr, env)
	}
}

// isHelpFlag reports whether arg asks for help.
func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}

// helpHelp is the help of the help command.
const helpHelp = `Usage: storycreep help [command]

Shows the list of commands, or the help of one command.
`

func help(args []string, stdout, stderr io.Writer) int {
	switch len(args) {
	case 0:
		printUsage(stdout)
		return ExitOK
	case 1:
		if isHelpFlag(args[0]) || args[0] == "help" {
			fmt.Fprint(stdout, helpHelp)
			return ExitOK
		}
		if strings.HasPrefix(args[0], "-") {
			return usageFailure(stderr, "help", fmt.Sprintf("unknown flag %s", args[0]))
		}
		c := lookup(args[0])
		if c == nil {
			return usageFailure(stderr, "help", fmt.Sprintf("unknown command %q", args[0]))
		}
		c.printHelp(stdout, c.flagSet())
		return ExitOK
	}
	return usageFailure(stderr, "help", "expected at most one command name")
}

func (c *command) flagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	// Parse errors are reported by execute, and help is printed by
	// printHelp, so the flag package prints nothing itself.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if c.loadsConfig {
		fs.String("config", "", "read the configuration file at `path`; overrides STORYCREEP_CONFIG_FILE")
	}
	if c.flags != nil {
		c.flags(fs)
	}
	return fs
}

func (c *command) execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer,
	env config.Environment) int {
	fs := c.flagSet()
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			c.printHelp(stdout, fs)
			return ExitOK
		}
		return usageFailure(stderr, c.name, err.Error())
	}
	// Parsing stops at the first argument, so a flag after the arguments
	// would be taken for one of them, and the command would refuse it with a
	// message that does not say why.
	for _, a := range fs.Args() {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") && name != "" && fs.Lookup(name) != nil {
			return usageFailure(stderr, c.name, fmt.Sprintf("the flag %s comes after the arguments; put the flags "+
				"first", a))
		}
	}
	if c.args == "" && fs.NArg() > 0 {
		return usageFailure(stderr, c.name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	var configFile string
	if c.loadsConfig {
		configFile = fs.Lookup("config").Value.String()
		// Load reads an empty path as no flag, so an empty --config
		// "$CONFIG" would silently check another file or none.
		if configFile == "" && isSet(fs, "config") {
			return usageFailure(stderr, c.name, "the -config path is empty")
		}
	}
	inv := &invocation{args: fs.Args(), stdin: stdin, stdout: stdout, stderr: stderr, flags: fs}

	if c.loadsConfig {
		cfg, err := config.Load(config.Options{File: configFile, Env: env})
		if err != nil {
			printConfigError(stderr, c.name, err)
			return ExitFailure
		}
		printWarnings(stderr, c.name, cfg.Warnings())
		inv.cfg = cfg
	}

	if err := c.run(ctx, inv); err != nil {
		var uerr usageError
		var cerr *config.Error
		switch {
		case errors.As(err, &uerr):
			return usageFailure(stderr, c.name, uerr.msg)
		case errors.As(err, &cerr):
			// A command that checks the configuration further, as
			// validate-config does, reports what it refuses as the frame
			// reports an invalid configuration.
			printConfigError(stderr, c.name, err)
			return ExitFailure
		}
		fmt.Fprintf(stderr, "storycreep %s: %v\n", c.name, err)
		return ExitFailure
	}
	return ExitOK
}

// boolFlag returns the value of the command's boolean flag name.
func (inv *invocation) boolFlag(name string) bool {
	return inv.flags.Lookup(name).Value.(flag.Getter).Get().(bool)
}

// isSet reports whether the command line set the named flag.
func isSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func printWarnings(w io.Writer, name string, warnings []string) {
	for _, msg := range warnings {
		fmt.Fprintf(w, "storycreep %s: warning: %s\n", name, msg)
	}
}

// printConfigError reports an invalid configuration. The warnings come
// first, because they often explain a problem: a misspelt variable is what
// leaves a required key unset.
func printConfigError(w io.Writer, name string, err error) {
	var cerr *config.Error
	if !errors.As(err, &cerr) {
		fmt.Fprintf(w, "storycreep %s: %v\n", name, err)
		return
	}
	printWarnings(w, name, cerr.Warnings)
	fmt.Fprintf(w, "storycreep %s: the configuration is invalid:\n", name)
	for _, p := range cerr.Problems {
		fmt.Fprintf(w, "  %s\n", p)
	}
	fmt.Fprintln(w, "The configuration reference, docs/configuration.md in the Storycreep repository, describes every key.")
}

// usageFailure reports a usage error of the named command, or of storycreep
// itself if name is empty, and returns ExitUsage.
func usageFailure(w io.Writer, name, msg string) int {
	prog := "storycreep"
	if name != "" {
		prog += " " + name
	}
	fmt.Fprintf(w, "%s: %s\n", prog, msg)
	if name == "" || name == "help" {
		fmt.Fprintln(w, "Run 'storycreep --help' for the list of commands.")
	} else {
		fmt.Fprintf(w, "Run '%s --help' for usage.\n", prog)
	}
	return ExitUsage
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `storycreep is the Storycreep server and its administration tool.

Usage:
  storycreep <command> [flags] [arguments]

Commands:
`)
	width := len("help")
	for _, c := range commands {
		width = max(width, len(c.name))
	}
	for _, c := range commands {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.name, c.summary)
	}
	fmt.Fprintf(w, "  %-*s  %s\n", width, "help", "Show the help of a command")
	fmt.Fprint(w, `
Run 'storycreep <command> --help' for the flags of a command.

Configuration comes from STORYCREEP_* environment variables and an optional
file. The configuration reference, docs/configuration.md in the Storycreep
repository, describes every key.

Exit status: 0 on success, 1 if the command fails or the configuration is
invalid, 2 if the command line is wrong.
`)
}

func (c *command) printHelp(w io.Writer, fs *flag.FlagSet) {
	synopsis := "storycreep " + c.name
	hasFlags := false
	fs.VisitAll(func(*flag.Flag) { hasFlags = true })
	if hasFlags {
		synopsis += " [flags]"
	}
	if c.args != "" {
		synopsis += " " + c.args
	}
	fmt.Fprintf(w, "Usage: %s\n\n%s\n", synopsis, strings.TrimSpace(c.help))
	if hasFlags {
		fmt.Fprint(w, "\nFlags:\n")
		fs.SetOutput(w)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}
}
