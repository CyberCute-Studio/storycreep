// Command fixturegen loads a reference fixture of PRD §9.1, small, medium,
// large, or dense, into a project of a running instance, for manual testing
// (NFR-Q-06, plan note N52), by sending its batches of commands to the
// project's command endpoint with an API token, as any client does.
//
// Create a new, empty project first, which only a session can (FR-AUTH-04),
// and an API token with the project:write scope, and save the token in a
// file. Then, in the server directory:
//
//	go run ./cmd/fixturegen -size medium -server http://localhost:8080 -project prj_… -token-file token.txt
//
// The same seed always gives the same IDs, whatever the size, so a project of
// an instance takes a seed that no other project of it was given, of any
// size (-seed, 1 by default). A load that stopped part way resumes where it
// stopped when it is run again with the same flags.
//
// With -out, it writes each batch's request body to a file of the directory,
// batch-001.json and on, and loads nothing. It writes into no directory that
// holds batch files already, whose batches beyond the fixture's would be
// left among them.
//
// It exits with status 1 if the fixture does not load, and with status 2 if
// a flag is wrong.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func main() {
	size := flag.String("size", "", "the fixture: small, medium, large, or dense")
	seed := flag.Uint64("seed", 1, "the seed, which no other project of the instance was loaded with, of any size")
	server := flag.String("server", "", "the instance's public URL, as http://localhost:8080")
	project := flag.String("project", "", "the ID of the new, empty project, prj_…, to load the fixture into")
	tokenFile := flag.String("token-file", "", "a file whose first line is an API token with the project:write scope")
	out := flag.String("out", "", "a directory to write the batches' request bodies to, instead of loading them")
	flag.Parse()

	s, err := fixture.ParseSize(*size)
	if err != nil {
		usage(err)
	}
	f, err := fixture.Generate(s, *seed)
	if err != nil {
		usage(err)
	}
	if *out != "" {
		if err := write(f, *out); err != nil {
			fail(err)
		}
		fmt.Fprintf(os.Stderr, "fixturegen: wrote the %d batches of the %s fixture, seed %d, to %s\n",
			len(f.Batches), s, *seed, *out)
		return
	}
	if *server == "" || *project == "" || *tokenFile == "" {
		usage(fmt.Errorf("-server, -project, and -token-file are needed, unless -out is given"))
	}
	// A URL that names no http or https server, as localhost:8080, would
	// otherwise fail each request, which the sender takes for a failure in
	// transit, and sends again, for seconds, before it gives up.
	if u, err := url.Parse(*server); err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		usage(fmt.Errorf("-server: %q is not the URL of an instance, as http://localhost:8080", *server))
	}
	if _, err := ids.Decode(ids.Project, *project); err != nil {
		usage(fmt.Errorf("-project: %w", err))
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		usage(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	start := time.Now()
	sender := fixture.Sender{
		Client:   &http.Client{Timeout: 5 * time.Minute},
		Endpoint: strings.TrimRight(*server, "/") + "/api/v1/projects/" + *project + "/commands",
		Header:   http.Header{"Authorization": {"Bearer " + token}},
		Progress: func(done, total int) {
			fmt.Fprintf(os.Stderr, "\rfixturegen: %d of %d batches applied", done, total)
		},
	}
	if err := sender.Send(ctx, f); err != nil {
		fmt.Fprintln(os.Stderr)
		var refused *fixture.RefusedError
		if errors.As(err, &refused) && refused.Status == http.StatusUnprocessableEntity {
			err = fmt.Errorf("%w\nA fixture's IDs are its seed's: if another project of the instance was loaded "+
				"with seed %d, of any size, load this one with another -seed", err, *seed)
		}
		fail(err)
	}
	fmt.Fprintf(os.Stderr, "\nfixturegen: loaded the %s fixture, seed %d, into %s in %v\n", s, *seed, *project,
		time.Since(start).Round(time.Second))
}

// readToken returns the first line of the file, without the white space
// around it.
func readToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("-token-file: %w", err)
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	sc.Scan()
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("-token-file: %w", err)
	}
	token := strings.TrimSpace(sc.Text())
	if token == "" {
		return "", fmt.Errorf("-token-file: %s has no token on its first line", path)
	}
	return token, nil
}

// write writes each batch's request body to a file of the directory dir,
// which holds no batch files yet.
func write(f *fixture.Fixture, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The directory's entries are matched by name, rather than by a pattern
	// of the directory's path, whose own *, ?, or [ would be read as a
	// pattern's.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if ok, _ := filepath.Match("batch-*.json", e.Name()); ok {
			return fmt.Errorf("-out: %s holds batch files already, as %s; give a new or empty directory", dir,
				e.Name())
		}
	}
	for i, b := range f.Batches {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("batch-%03d.json", i+1)), fixture.Body(b),
			0o644); err != nil {
			return err
		}
	}
	return nil
}

func usage(err error) {
	fmt.Fprintln(os.Stderr, "fixturegen:", err)
	flag.Usage()
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fixturegen:", err)
	os.Exit(1)
}
