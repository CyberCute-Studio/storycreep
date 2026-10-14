// Command storycreep is the Storycreep server and its administration tool.
// Run it with --help for the list of commands.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/CyberCute-Studio/storycreep/server/internal/cli"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, config.Environment{})
	stop()
	os.Exit(code)
}
