// Command i18nextract is catalog extraction (PRD FR-I18N-01 AC3): it reads
// the message keys that the code uses, and rewrites the message catalogs to
// hold exactly those keys, sorted, so that running it again changes nothing.
// See package extract.
//
// With -check, it rewrites nothing, and reports the catalogs that it would
// rewrite. Nor does it rewrite the catalogs of code that it cannot read whole,
// as when a file does not parse, until that is fixed. It prints each problem as "file:line: message", such as a key that
// the source catalog has no message for, and exits with status 1 if there is
// any or, with -check, if a catalog is out of date. It exits with status 2 if
// a file cannot be read or written. Run it from the server directory, or pass
// the root.
package main

import (
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n/extract"
)

func main() {
	root := flag.String("root", "..", "the repository root")
	check := flag.Bool("check", false, "report the catalogs that are out of date instead of rewriting them")
	flag.Parse()

	res, err := extract.Run(*root, extract.Catalogs, !*check)
	if err != nil {
		fmt.Fprintln(os.Stderr, "i18nextract:", err)
		os.Exit(2)
	}
	for _, path := range res.Changed {
		switch {
		case *check:
			fmt.Fprintf(os.Stderr, "%s is out of date; run pnpm i18n:extract\n", path)
		case slices.Contains(res.Held, path):
			fmt.Fprintf(os.Stderr, "%s is out of date, but is not rewritten until the problems below "+
				"that keep extraction from reading every key are fixed\n", path)
		default:
			fmt.Printf("wrote %s\n", path)
		}
	}
	for _, p := range res.Problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(res.Problems) > 0 || *check && len(res.Changed) > 0 {
		os.Exit(1)
	}
}
