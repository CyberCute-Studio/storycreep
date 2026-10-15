// Package gitexec runs git for the checks that compare the working tree with
// the history of the main branch: that merged migrations are unchanged
// (package migrationcheck), and that the API breaks no client of the main
// branch's (package apicontract).
package gitexec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Run runs git in dir, with stdin as its input, and returns its output. Its
// error says what git wrote to stderr.
func Run(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// ForkPoint returns the commit where the current branch of the repository
// that holds dir forked from base, such as origin/main: their merge base. It
// needs the history of both, so a shallow clone needs its full history
// fetched first, as its error says.
func ForkPoint(ctx context.Context, dir, base string) (string, error) {
	out, err := Run(ctx, dir, "", "merge-base", "HEAD", base)
	if err != nil {
		return "", fmt.Errorf("find where HEAD forked from %s: %w; a shallow clone needs its full history "+
			"(git fetch --unshallow)", base, err)
	}
	return strings.TrimSpace(out), nil
}
