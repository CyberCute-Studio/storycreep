package httpserver

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
)

// appPrefix is where the server serves the tool's files.
const appPrefix = "/app/"

// appAssets is the directory of the tool's bundle where Vite writes the files
// whose names hold a hash of their content, which therefore never change.
const appAssets = "assets/"

// noncePlaceholder is what the tool's entry page, index.html, holds where
// each response of it has the nonce of the response's
// Content-Security-Policy: Vite writes it as the nonce of the page's scripts
// and styles, and of a meta element that the app reads it from
// (web/app/vite.config.ts).
const noncePlaceholder = "__STORYCREEP_CSP_NONCE__"

// immutable is the Cache-Control of the files under appAssets, which a
// browser may keep for a year without asking again.
const immutable = "public, max-age=31536000, immutable"

// registerPages registers the pages of the web interface, whose files the
// binary embeds (package webassets), app the tool's and site the public
// site's: the tool's files under /app/, and at / the public site's home page
// if publicSite, the PublicSite capability, is on, or else the tool's entry
// page, which is where its sign-in screen will be (PRD
// FR-PUB-04). The tool's entry page is served for every path under /app/
// that is not a file of its bundle, but for one that names a file, whose last
// segment has an extension, as a file of an older bundle does, so that the
// app's router shows the page of a link to it. Until plan item 108 builds the
// site, its home page is a placeholder, and that item adds the routes that it
// needs.
func registerPages(m *api.Mux, app, site fs.FS, publicSite bool) {
	entry, err := fs.ReadFile(app, "index.html")
	if err != nil {
		// The bundle and the placeholder both have one, which TestPages
		// checks of the placeholder, and the image's tests of the bundle.
		panic(err)
	}
	m.HandlePage("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicSite {
			serveFile(w, r, site, "index.html", "")
			return
		}
		serveEntry(w, entry)
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
		switch {
		case name == "" || name == "index.html":
			serveEntry(w, entry)
		case !fs.ValidPath(strings.TrimSuffix(name, "/")):
			// A path with . or .. in it, or an empty segment, names no page.
			api.WriteError(w, r, api.NewError(api.CodeNotFound))
		case name == strings.TrimSuffix(appAssets, "/") || strings.HasPrefix(name, appAssets):
			serveFile(w, r, app, name, immutable)
		case isFile(app, name):
			serveFile(w, r, app, name, "")
		case path.Ext(path.Base(name)) != "":
			api.WriteError(w, r, api.NewError(api.CodeNotFound))
		default:
			// A page of the app, which its router shows, or says that it
			// does not have.
			serveEntry(w, entry)
		}
	}))
}

// entryPolicy is the Content-Security-Policy of a response of the tool's
// entry page: the server's, with the response's nonce added to the sources
// of scripts and styles (PRD NFR-SEC-09), which the page's own scripts and
// styles carry, and which the app gives the styles that a library adds to
// the page, as a dialog's scroll lock.
func entryPolicy(nonce string) string {
	source := "'self' 'nonce-" + nonce + "'"
	return contentSecurityPolicy + "; script-src " + source + "; style-src " + source
}

// serveEntry answers with the tool's entry page, page, whose nonce
// placeholders hold a new nonce, which the response's Content-Security-Policy
// allows. The nonce is 130 random bits, as base32, whose letters and digits
// are among those of a nonce's base64 (CSP Level 3). The page is never
// stored, since each response has a nonce of its own.
func serveEntry(w http.ResponseWriter, page []byte) {
	nonce := rand.Text()
	body := bytes.ReplaceAll(page, []byte(noncePlaceholder), []byte(nonce))
	h := w.Header()
	h.Set("Content-Security-Policy", entryPolicy(nonce))
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	// An error here means that the client has gone.
	_, _ = w.Write(body)
}

// isFile reports whether fsys has a file, rather than a directory, at name.
func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// serveFile answers r with the file name of fsys, or with not_found if there
// is no such file, as for a directory or a path with a .. in it, with the
// Cache-Control cache if it is set. It copies the file to the response as it
// reads it, without a copy of its own.
func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name, cache string) {
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
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if cache != "" {
		h.Set("Cache-Control", cache)
	}
	// An error here comes after the status was sent, and leaves the body short
	// of its Content-Length, which tells the client.
	_, _ = io.Copy(w, f)
}
