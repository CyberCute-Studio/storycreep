package httpserver

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/webassets"
)

// appPrefix is where the server serves the tool's files.
const appPrefix = "/app/"

// registerPages registers the pages of the web interface, which the binary
// embeds (package webassets): the tool's files under /app/, and at / the
// public site's home page if the PublicSite capability is on, or else the
// tool's entry page, which is where its sign-in screen will be (PRD
// FR-PUB-04). Until plan items 53 and 108 build the tool and the site, each
// is one placeholder page, and those items add the routes they need.
func registerPages(m *api.Mux, publicSite bool) {
	app := webassets.App()
	home := app
	if publicSite {
		home = webassets.Site()
	}
	m.HandlePage("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFile(w, r, home, "index.html")
	}))
	// The tool's relative links resolve against /app/, so /app redirects
	// there.
	m.HandlePage("/app", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := appPrefix
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}))
	m.HandlePage(appPrefix+"*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, appPrefix)
		if name == "" {
			name = "index.html"
		}
		serveFile(w, r, app, name)
	}))
}

// serveFile answers r with the file name of fsys, or with not_found if there
// is no such file, as for a directory or a path with a .. in it. It copies
// the file to the response as it reads it, without a copy of its own.
func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	var info fs.FileInfo
	if err == nil {
		defer f.Close()
		info, err = f.Stat()
	}
	if err == nil && info.IsDir() {
		err = fs.ErrNotExist
	}
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrInvalid):
		api.WriteError(w, r, api.NewError(api.CodeNotFound))
		return
	case err != nil:
		api.WriteError(w, r, err)
		return
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	// An error here comes after the status was sent, and leaves the body short
	// of its Content-Length, which tells the client.
	_, _ = io.Copy(w, f)
}
