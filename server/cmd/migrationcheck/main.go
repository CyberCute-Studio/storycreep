// Command migrationcheck fails if the working tree edits, deletes, or renames
// a migration that is on the main branch (PRD FR-ADMIN-04 AC2). It compares
// the migrations with the ones at the commit where the branch forked from
// -base, so it needs the git history of both; in a shallow clone, fetch it
// first.
//
// It prints each problem as "path: message" and exits with status 1 if there
// is any, or with status 2 if the check cannot run. Run it from the server
// directory, or pass -dir.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CyberCute-Studio/storycreep/server/internal/migrationcheck"
)

func main() {
	dir := flag.String("dir", filepath.Join("internal", "database", "migrations"), "the migrations directory")
	base := flag.String("base", "origin/main", "the main branch to compare with")
	flag.Parse()

	res, err := migrationcheck.Check(context.Background(), *dir, *base)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrationcheck:", err)
		os.Exit(2)
	}
	for _, p := range res.Problems {
		fmt.Fprintf(os.Stderr, "%s: %s\n", filepath.ToSlash(filepath.Join(*dir, p.Name)), p.Msg)
	}
	if len(res.Problems) > 0 {
		fmt.Fprintf(os.Stderr, "migrationcheck: %d problem(s)\n", len(res.Problems))
		os.Exit(1)
	}
	fmt.Printf("migrationcheck: the %d migration(s) on %s at %.12s are unchanged\n", res.Checked, *base, res.Base)
}
