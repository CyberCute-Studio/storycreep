package httpserver

import (
	"bytes"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/webassets"
)

// entryNonce is a nonce of the entry page's Content-Security-Policy:
// rand.Text's 26 characters of base32.
var entryNonce = regexp.MustCompile(`'nonce-([A-Z2-7]{26})'`)

// checkEntry checks that res is the tool's entry page, page, with the
// placeholders of its nonce filled in with the nonce of its
// Content-Security-Policy, which allows the page's scripts and styles with
// it, and returns the nonce.
func checkEntry(t *testing.T, method, path string, res response, page []byte) string {
	t.Helper()
	if res.status != http.StatusOK {
		t.Errorf("%s %s: status %d, want 200: %s", method, path, res.status, res.body)
		return ""
	}
	if ct := res.header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("%s %s: Content-Type %q, want text/html; charset=utf-8", method, path, ct)
	}
	if cc := res.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("%s %s: Cache-Control %q, want no-store: each response has a nonce of its own", method, path, cc)
	}
	csp := res.header.Get("Content-Security-Policy")
	m := entryNonce.FindStringSubmatch(csp)
	if m == nil || csp != entryPolicy(m[1]) {
		t.Errorf("%s %s: Content-Security-Policy %q, want the entry page's, with a nonce", method, path, csp)
		return ""
	}
	nonce := m[1]
	want := bytes.ReplaceAll(page, []byte(noncePlaceholder), []byte(nonce))
	if got := res.header.Get("Content-Length"); got != strconv.Itoa(len(want)) {
		t.Errorf("%s %s: Content-Length %s, want %d", method, path, got, len(want))
	}
	if method == http.MethodGet && !bytes.Equal(res.body, want) {
		t.Errorf("GET %s: the body is not the entry page with the nonce %s:\n%s", path, nonce, res.body)
	}
	return nonce
}

// TestPages checks the pages that the binary embeds: the tool under /app/,
// and at / the public site's home page or, with the public site off, the
// tool's entry page, with a nonce of its own in each response, which its
// Content-Security-Policy allows the page's scripts and styles with (PRD
// NFR-SEC-09). They are served while the server starts, because they need no
// database.
//
// Verifies: NFR-SEC-09, FR-PUB-04/AC3
func TestPages(t *testing.T) {
	appIndex, err := fs.ReadFile(webassets.App(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	siteIndex, err := fs.ReadFile(webassets.Site(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(appIndex, siteIndex) {
		t.Fatal("the tool's and the site's index.html are the same, so the test cannot tell them apart")
	}
	if !bytes.Contains(appIndex, []byte(`nonce="`+noncePlaceholder+`"`)) {
		t.Fatalf("the tool's index.html has no nonce placeholder, %s, so the test cannot check it", noncePlaceholder)
	}

	for _, tt := range []struct {
		name, publicSite string
	}{
		{"public site off", "off"},
		{"public site on", "on"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(loadConfig(t, map[string]string{"STORYCREEP_PUBLIC_SITE": tt.publicSite}),
				slog.New(slog.DiscardHandler))
			s.ready.setPhase(phaseStarting)
			nonces := map[string]bool{}
			entries := []string{
				"/app/", "/app/?x=1", "/app/index.html", "/app/projects", "/app/projects/",
				"/app/projects/prj_0123/boards/brd_4567?view=script#node",
			}
			if tt.publicSite == "off" {
				entries = append(entries, "/", "/?from=somewhere")
			}
			for _, path := range entries {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					nonce := checkEntry(t, method, path, do(s.main, method, path, nil), appIndex)
					if nonce != "" && nonces[nonce] {
						t.Errorf("%s %s: the nonce %s of an earlier response", method, path, nonce)
					}
					nonces[nonce] = true
				}
			}
			if tt.publicSite == "on" {
				for _, path := range []string{"/", "/?from=somewhere"} {
					res := do(s.main, http.MethodGet, path, nil)
					if res.status != http.StatusOK || !bytes.Equal(res.body, siteIndex) {
						t.Errorf("GET %s: status %d, want 200 and the site's home page: %s", path, res.status, res.body)
					}
					if csp := res.header.Get("Content-Security-Policy"); csp != contentSecurityPolicy {
						t.Errorf("GET %s: Content-Security-Policy %q, want the server's", path, csp)
					}
				}
			}

			for path, want := range map[string]string{"/app": "/app/", "/app?x=1&y=2": "/app/?x=1&y=2"} {
				res := do(s.main, http.MethodGet, path, nil)
				if res.status != http.StatusMovedPermanently || res.header.Get("Location") != want {
					t.Errorf("GET %s: status %d, Location %q; want 301 to %s", path, res.status,
						res.header.Get("Location"), want)
				}
			}

			for _, path := range []string{
				"/app/missing.js", "/app/index.html/", "/app/../site/index.html", "/app/%2e%2e/site/index.html",
				"/app/.", "/app/..", "/app/a//b", "/index.html", "/app/assets", "/app/assets/", "/app/assets/x",
			} {
				res := do(s.main, http.MethodGet, path, nil)
				if res.status != http.StatusNotFound || res.code() != api.CodeNotFound {
					t.Errorf("GET %s: status %d, code %q; want 404 not_found: %s", path, res.status, res.code(),
						res.body)
				}
			}

			res := do(s.main, http.MethodPost, "/", nil)
			if res.status != http.StatusMethodNotAllowed || res.code() != api.CodeMethodNotAllowed {
				t.Errorf("POST /: status %d, code %q; want 405 method_not_allowed", res.status, res.code())
			}
		})
	}
}

// TestPagesOfBundle checks how the files of a bundle such as Vite builds are
// served: those under assets/, whose names hold a hash of their content, for
// a year, and the others as every response, never stored; a path of a file
// that the bundle does not have, as one of an older bundle, with not_found;
// and any other path with the entry page, whose route the app's router
// shows.
func TestPagesOfBundle(t *testing.T) {
	page := []byte(`<!doctype html><meta property="csp-nonce" nonce="` + noncePlaceholder + `">` +
		`<script type="module" src="/app/assets/index-abc.js" nonce="` + noncePlaceholder + `"></script>`)
	app := fstest.MapFS{
		"index.html":           {Data: page},
		"favicon.svg":          {Data: []byte("<svg></svg>")},
		"assets/index-abc.js":  {Data: []byte("export {};")},
		"assets/index-abc.css": {Data: []byte("body{}")},
	}
	m := api.NewMux(api.MuxOptions{
		Middleware: []func(http.Handler) http.Handler{securityHeaders(false)},
	})
	registerPages(m, app, fstest.MapFS{"index.html": {Data: []byte("site")}}, false)

	for path, want := range map[string]struct{ ctype, cache, body string }{
		"/app/assets/index-abc.js":  {"text/javascript; charset=utf-8", immutable, "export {};"},
		"/app/assets/index-abc.css": {"text/css; charset=utf-8", immutable, "body{}"},
		"/app/favicon.svg":          {"image/svg+xml", "no-store", "<svg></svg>"},
	} {
		res := do(m, http.MethodGet, path, nil)
		if res.status != http.StatusOK || string(res.body) != want.body {
			t.Errorf("GET %s: status %d, want 200 and the file: %s", path, res.status, res.body)
		}
		if got := res.header.Get("Content-Type"); got != want.ctype {
			t.Errorf("GET %s: Content-Type %q, want %q", path, got, want.ctype)
		}
		if got := res.header.Get("Cache-Control"); got != want.cache {
			t.Errorf("GET %s: Cache-Control %q, want %q", path, got, want.cache)
		}
	}

	for _, path := range []string{"/app/", "/app/index.html", "/app/projects/prj_1", "/app/favicon", "/"} {
		checkEntry(t, http.MethodGet, path, do(m, http.MethodGet, path, nil), page)
	}

	for _, path := range []string{
		"/app/assets/index-old.js", "/app/index-abc.js", "/app/projects/old.css", "/app/assets/index-abc.js/",
	} {
		res := do(m, http.MethodGet, path, nil)
		if res.status != http.StatusNotFound || res.code() != api.CodeNotFound {
			t.Errorf("GET %s: status %d, code %q; want 404 not_found", path, res.status, res.code())
		}
		if strings.Contains(string(res.body), "<script") {
			t.Errorf("GET %s: the entry page, which a browser would not run as the script it asked for", path)
		}
	}
}
