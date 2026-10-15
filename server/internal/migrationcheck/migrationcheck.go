// Package migrationcheck enforces that a migration is immutable once it is on
// the main branch (PRD FR-ADMIN-04 AC2). A database that applied a migration
// never applies it again, so an edited migration would run in its new form
// only on fresh installs, and the upgrade test, which seeds from the main
// branch's migrations, would never run it either. Deleting or renaming one
// breaks every database that applied it. Adding migrations is the only
// change allowed.
package migrationcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing/fstest"
)

// Problem is a migration on the base that the change edits or removes.
type Problem struct {
	Name string
	Msg  string
}

func (p Problem) String() string {
	return p.Name + ": " + p.Msg
}

// Compare returns a problem for each migration, a .sql file, in base that
// head does not have with the same content. Migrations that only head has
// are new, which is allowed.
func Compare(base, head fs.FS) ([]Problem, error) {
	names, err := fs.Glob(base, "*.sql")
	if err != nil {
		return nil, err
	}
	var problems []Problem
	for _, name := range names {
		was, err := fs.ReadFile(base, name)
		if err != nil {
			return nil, err
		}
		is, err := fs.ReadFile(head, name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			problems = append(problems, Problem{name, "is on the main branch and was deleted or renamed; " +
				"a merged migration is never removed, so restore it"})
		case err != nil:
			return nil, err
		case !bytes.Equal(was, is):
			problems = append(problems, Problem{name, "is on the main branch and was changed; a merged migration " +
				"is never edited, so restore it and add a new migration for the change"})
		}
	}
	return problems, nil
}

// Result is the outcome of Check.
type Result struct {
	// Base is the commit the migrations were compared with.
	Base string
	// Checked is the number of migrations on Base.
	Checked  int
	Problems []Problem
}

// Check compares the migrations in the directory dir, as they are in the
// working tree, with the ones at the commit where the current branch forked
// from base, such as origin/main. Comparing with the fork point rather than
// base itself keeps migrations that base gained since from counting as
// deleted. dir must be in a git repository with the history of both, so a
// shallow clone needs its full history fetched first.
func Check(ctx context.Context, dir, base string) (Result, error) {
	fork, err := git(ctx, dir, "merge-base", "HEAD", base)
	if err != nil {
		return Result{}, fmt.Errorf("find where HEAD forked from %s: %w; a shallow clone needs its full history "+
			"(git fetch --unshallow)", base, err)
	}
	fork = strings.TrimSpace(fork)
	baseFS, err := Tree(ctx, dir, fork)
	if err != nil {
		return Result{}, err
	}
	head, err := Tree(ctx, dir, "")
	if err != nil {
		return Result{}, err
	}
	problems, err := Compare(baseFS, head)
	if err != nil {
		return Result{}, err
	}
	names, _ := fs.Glob(baseFS, "*.sql")
	return Result{Base: fork, Checked: len(names), Problems: problems}, nil
}

// Tree returns the files directly in the directory dir as they are at the
// commit rev, or in the working tree if rev is empty. A directory that does
// not exist at rev, such as before the first migration, has no files.
func Tree(ctx context.Context, dir, rev string) (fs.FS, error) {
	if rev == "" {
		return os.DirFS(dir), nil
	}
	files := fstest.MapFS{}
	out, err := git(ctx, dir, "ls-tree", "-z", rev, "--", ".")
	if err != nil {
		return nil, fmt.Errorf("list the migrations at %s: %w", rev, err)
	}
	for entry := range strings.SplitSeq(strings.TrimSuffix(out, "\x00"), "\x00") {
		if entry == "" {
			continue
		}
		// <mode> SP <type> SP <object> TAB <path>
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("list the migrations at %s: unexpected git ls-tree output %q", rev, entry)
		}
		if fields[1] != "blob" || path.Base(name) != name {
			continue
		}
		content, err := git(ctx, dir, "cat-file", "blob", fields[2])
		if err != nil {
			return nil, fmt.Errorf("read %s at %s: %w", name, rev, err)
		}
		files[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return files, nil
}

// git runs git in dir and returns its output.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
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
