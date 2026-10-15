// Command apicompat fails if the working tree's OpenAPI document breaks the
// clients of the one on the main branch (PRD FR-API-01 AC3). It compares the
// document with the one at the commit where the branch forked from -base, so
// it needs the git history of both; in a shallow clone, fetch it first.
//
// Until the first release, a breaking change passes if a commit since the
// fork point gives a reason for it in an "API-Break: <reason>" trailer.
//
// It prints each change as "operation: message" and exits with status 1 if
// there is any and no commit gives a reason, or with status 2 if the check
// cannot run. Run it from the server directory, or pass -doc.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CyberCute-Studio/storycreep/server/internal/apicontract"
)

func main() {
	doc := flag.String("doc", filepath.Join("..", "schema", "api", "v1", "openapi.json"), "the OpenAPI document")
	base := flag.String("base", "origin/main", "the main branch to compare with")
	flag.Parse()

	res, err := apicontract.Check(context.Background(), *doc, *base)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apicompat:", err)
		os.Exit(2)
	}
	switch {
	case res.Missing:
		fmt.Printf("apicompat: there is no OpenAPI document on %s at %.12s, so there is nothing to compare with\n",
			*base, res.Base)
	case len(res.Changes) == 0:
		fmt.Printf("apicompat: the API is compatible with the one on %s at %.12s\n", *base, res.Base)
	case len(res.Overrides) > 0:
		for _, c := range res.Changes {
			fmt.Println(c)
		}
		for _, o := range res.Overrides {
			fmt.Printf("apicompat: %.12s allows breaking the API: %s\n", o.Commit, o.Reason)
		}
		fmt.Printf("apicompat: %d change(s) break the clients of the API on %s, as the commits above allow\n",
			len(res.Changes), *base)
	default:
		for _, c := range res.Changes {
			fmt.Fprintln(os.Stderr, c)
		}
		fmt.Fprintf(os.Stderr, "apicompat: %d change(s) break the clients of the API on %s. Undo them, or, until "+
			"the first release, say why the API may break in a trailer of one of the branch's commits: "+
			"\"%s: <reason>\"\n", len(res.Changes), *base, apicontract.OverrideTrailer)
		os.Exit(1)
	}
}
