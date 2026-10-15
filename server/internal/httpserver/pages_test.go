package httpserver

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/webassets"
)

// TestPages checks the pages that the binary embeds: the tool under /app/,
// and at / the public site's home page or, with the public site off, the
// tool's entry page. They are served while the server starts, because they
// need no database.
func TestPages(t *testing.T) {
	appIndex, err := fs.ReadFile(webassets.App(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	siteIndex, err := fs.ReadFile(webassets.Site(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if string(appIndex) == string(siteIndex) {
		t.Fatal("the tool's and the site's index.html are the same, so the test cannot tell them apart")
	}

	for _, tt := range []struct {
		name, publicSite string
		home             []byte
	}{
		{"public site off", "off", appIndex},
		{"public site on", "on", siteIndex},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(loadConfig(t, map[string]string{"STORYCREEP_PUBLIC_SITE": tt.publicSite}),
				slog.New(slog.DiscardHandler))
			s.ready.setPhase(phaseStarting)
			for path, want := range map[string][]byte{
				"/":                appIndex,
				"/app/":            appIndex,
				"/app/?x=1":        appIndex,
				"/app/index.html":  appIndex,
				"/?from=somewhere": appIndex,
			} {
				if path == "/" || strings.HasPrefix(path, "/?") {
					want = tt.home
				}
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					res := do(s.main, method, path, nil)
					if res.status != http.StatusOK {
						t.Errorf("%s %s: status %d, want 200: %s", method, path, res.status, res.body)
						continue
					}
					if ct := res.header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
						t.Errorf("%s %s: Content-Type %q, want text/html; charset=utf-8", method, path, ct)
					}
					if csp := res.header.Get("Content-Security-Policy"); csp != contentSecurityPolicy {
						t.Errorf("%s %s: Content-Security-Policy %q, want the server's", method, path, csp)
					}
					if method == http.MethodGet && string(res.body) != string(want) {
						t.Errorf("GET %s: the body is not the expected page:\n%s", path, res.body)
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
				"/app/.", "/index.html",
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
