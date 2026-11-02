package httpserver

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
)

// appPrefix is where the server serves the tool's files.
const appPrefix = "/app/"

// appAssets is the directory of the tool's bundle where Vite writes the files
// whose names hold a hash of their content, which therefore never change
// (assetsDir, in web/app/vite.config.ts, which refuses a file of the app's
// public/ there, whose name has none).
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

// fileType is the Content-Type of a file of a bundle, and whether the file
// compresses, as text does, and images and fonts, already compressed, do
// not.
type fileType struct {
	contentType string
	compresses  bool
}

// fileTypes are the types of the files of a bundle, by extension. The server
// names them itself rather than read the operating system's tables, as
// mime.TypeByExtension does, which may name another type: macOS's
// /etc/apache2/mime.types names application/javascript for .js, and a
// Windows registry may name any type for .css, which a browser then refuses
// as a stylesheet, under nosniff. A file of another extension is
// application/octet-stream, whatever the host, and does not compress: a type
// that a bundle needs joins the table, as TestBundleFileTypes, which checks
// each file of the web app's build, says.
var fileTypes = map[string]fileType{
	".avif":        {"image/avif", false},
	".css":         {"text/css; charset=utf-8", true},
	".gif":         {"image/gif", false},
	".html":        {"text/html; charset=utf-8", true},
	".ico":         {"image/vnd.microsoft.icon", true},
	".jpeg":        {"image/jpeg", false},
	".jpg":         {"image/jpeg", false},
	".js":          {"text/javascript; charset=utf-8", true},
	".json":        {"application/json", true},
	".map":         {"application/json", true},
	".mjs":         {"text/javascript; charset=utf-8", true},
	".otf":         {"font/otf", true},
	".png":         {"image/png", false},
	".svg":         {"image/svg+xml", true},
	".ttf":         {"font/ttf", true},
	".txt":         {"text/plain; charset=utf-8", true},
	".wasm":        {"application/wasm", true},
	".webmanifest": {"application/manifest+json", true},
	".webp":        {"image/webp", false},
	".woff":        {"font/woff", false},
	".woff2":       {"font/woff2", false},
	".xml":         {"text/xml; charset=utf-8", true},
}

// typeOf returns the type of the file name.
func typeOf(name string) fileType {
	if t, ok := fileTypes[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return fileType{contentType: "application/octet-stream"}
}

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
	appFiles, siteFiles := newFiles(app), newFiles(site)
	m.HandlePage("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicSite {
			siteFiles.serve(w, r, "index.html", "")
			return
		}
		serveEntry(w, entry)
	}))
	// The tool's relative links resolve against /app/, so /app redirects
	// there, and so does /app/index.html, whose path the app's router has no
	// page of.
	toApp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := appPrefix
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	m.HandlePage("/app", toApp)
	m.HandlePage(appPrefix+"*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, appPrefix)
		switch {
		case name == "":
			serveEntry(w, entry)
		case name == "index.html":
			toApp(w, r)
		case !fs.ValidPath(strings.TrimSuffix(name, "/")):
			// A path with . or .. in it, or an empty segment, names no page.
			api.WriteError(w, r, api.NewError(api.CodeNotFound))
		case name == strings.TrimSuffix(appAssets, "/") || strings.HasPrefix(name, appAssets):
			appFiles.serve(w, r, name, immutable)
		case isFile(app, name):
			appFiles.serve(w, r, name, "")
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

// files serves the files of a bundle, fsys, which never change, since the
// binary embeds them. It keeps the gzip of each file that compresses once it
// has made it, for each later request that accepts it.
type files struct {
	fsys fs.FS
	mu   sync.Mutex
	gzip map[string]*gzipped
}

// gzipped is the gzip of a file once it is made: nil if the file does not
// compress to less than its size, or could not be read.
type gzipped struct {
	once sync.Once
	data []byte
}

// newFiles returns the files of fsys.
func newFiles(fsys fs.FS) *files {
	return &files{fsys: fsys, gzip: map[string]*gzipped{}}
}

// serve answers r with the file name, or with not_found if there is no such
// file, as for a directory or a path with a .. in it, with the
// Cache-Control cache if it is set. A file that compresses goes as its gzip
// to a request that accepts gzip, and as it is to one that does not, with a
// Vary that says so. Otherwise serve copies the file to the response as it
// reads it, without a copy of its own.
func (b *files) serve(w http.ResponseWriter, r *http.Request, name, cache string) {
	f, err := b.fsys.Open(name)
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
	t := typeOf(name)
	h := w.Header()
	h.Set("Content-Type", t.contentType)
	if cache != "" {
		h.Set("Cache-Control", cache)
	}
	if t.compresses {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Values("Accept-Encoding")) {
			if data := b.gzipOf(name); data != nil {
				h.Set("Content-Encoding", "gzip")
				h.Set("Content-Length", strconv.Itoa(len(data)))
				// An error here means that the client has gone.
				_, _ = w.Write(data)
				return
			}
		}
	}
	h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	// An error here comes after the status was sent, and leaves the body short
	// of its Content-Length, which tells the client.
	_, _ = io.Copy(w, f)
}

// gzipOf returns the gzip of the file name, made when it is first asked
// for, or nil if the file does not compress to less than its size.
func (b *files) gzipOf(name string) []byte {
	b.mu.Lock()
	g := b.gzip[name]
	if g == nil {
		// Only a file that exists gets here, so the map holds at most one
		// entry for each file of the bundle.
		g = &gzipped{}
		b.gzip[name] = g
	}
	b.mu.Unlock()
	g.once.Do(func() {
		data, err := fs.ReadFile(b.fsys, name)
		if err != nil {
			return
		}
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return
		}
		if _, err := zw.Write(data); err != nil || zw.Close() != nil {
			return
		}
		if buf.Len() < len(data) {
			g.data = buf.Bytes()
		}
	})
	return g.data
}

// acceptsGzip reports whether the values of a request's Accept-Encoding
// header accept gzip: as gzip, or x-gzip, its old name, or else as *, with a
// weight, q, above 0 (RFC 9110, section 12.5.3). A weight that is not a
// number from 0 to 1 refuses it.
func acceptsGzip(values []string) bool {
	named, star := -1.0, -1.0
	for _, value := range values {
		for coding := range strings.SplitSeq(value, ",") {
			name, params, _ := strings.Cut(coding, ";")
			q := 1.0
			for param := range strings.SplitSeq(params, ";") {
				key, weight, ok := strings.Cut(param, "=")
				if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
					var err error
					if q, err = strconv.ParseFloat(strings.TrimSpace(weight), 64); err != nil || !(q >= 0 && q <= 1) {
						q = 0
					}
				}
			}
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "gzip", "x-gzip":
				named = max(named, q)
			case "*":
				star = max(star, q)
			}
		}
	}
	if named >= 0 {
		return named > 0
	}
	return star > 0
}
