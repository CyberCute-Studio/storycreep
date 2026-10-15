// Command traceability writes the requirement traceability report (PRD
// NFR-Q-02): the tests that verify each requirement, and the acceptance
// criteria of the current phase that no test verifies. Tests name what they
// verify in a "Verifies:" comment; see package traceability.
//
// Without -out, it writes the report as Markdown to standard output. With
// -out, it writes traceability.md and traceability.json into that directory
// and a one-line summary to standard output.
//
// It prints each problem as "file:line: message" and exits with status 1 if
// there is any: a Verifies comment it cannot read, one that names an unknown
// requirement or criterion or is not directly above a test, or a problem in
// the PRD. It exits with status 2 if the phase is unknown or a file cannot be
// read or written. A criterion without a test does not change the exit
// status; the report is informational until the MVP release (plan item 119).
// Run it from the server directory, or pass the paths.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CyberCute-Studio/storycreep/server/internal/traceability"
)

func main() {
	prd := flag.String("prd", filepath.Join("..", "docs", "PRD.md"), "path to the PRD")
	root := flag.String("root", "..", "the repository root, which is scanned for tests")
	phase := flag.String("phase", "MVP", "the current phase; its acceptance criteria and those of earlier phases are in scope")
	out := flag.String("out", "", "a directory to write traceability.md and traceability.json into")
	flag.Parse()

	report, problems, err := traceability.Generate(*prd, *root, *phase)
	if err != nil {
		fail(err)
	}
	if *out == "" {
		if _, err := os.Stdout.Write(report.Markdown()); err != nil {
			fail(err)
		}
	} else {
		js, err := report.JSON()
		if err != nil {
			fail(err)
		}
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(filepath.Join(*out, "traceability.md"), report.Markdown(), 0o644); err != nil {
			fail(err)
		}
		if err := os.WriteFile(filepath.Join(*out, "traceability.json"), js, 0o644); err != nil {
			fail(err)
		}
		fmt.Println(report.SummaryLine())
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "traceability: %d problem(s)\n", len(problems))
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "traceability:", err)
	os.Exit(2)
}
