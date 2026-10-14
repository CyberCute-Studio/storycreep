// Command doccheck checks the PRD and the implementation plan against the
// rules they state about themselves: the PRD's requirement dependency graph
// and acceptance criteria (§4.0), and the plan's ordering, coverage, and
// reference rules.
//
// It prints each problem as "file:line: message" and exits with status 1 if
// there is any, or with status 2 if a document cannot be read. Run it from the
// server directory, or pass both paths.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CyberCute-Studio/storycreep/server/internal/doccheck"
)

func main() {
	prd := flag.String("prd", filepath.Join("..", "docs", "PRD.md"), "path to the PRD")
	plan := flag.String("plan", filepath.Join("..", "docs", "IMPLEMENTATION_PLAN.md"), "path to the implementation plan")
	flag.Parse()

	problems, err := doccheck.CheckFiles(*prd, *plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "doccheck:", err)
		os.Exit(2)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "doccheck: %d problem(s)\n", len(problems))
		os.Exit(1)
	}
}
