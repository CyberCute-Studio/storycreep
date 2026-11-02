// Package webassets holds the static files of the web interface, embedded in
// the binary, so that the server serves them without a Node process and the
// container image needs no other files (PRD §10.6, §10.8, FR-PUB-01 AC3): the
// tool's bundle, and the public site's.
//
// The tool's bundle is the build of web/app, which Vite writes to dist/app,
// and which the binary embeds when it is built with the webdist tag, as the
// container image is (deploy/Dockerfile):
//
//	pnpm --filter @storycreep/app build
//	go build -tags webdist ./cmd/storycreep
//
// A binary built without the tag, as by go test, go vet, or the development
// compose file, embeds app/index.html instead, a placeholder page that says
// so, which needs no Node toolchain to build. Until the site is built (plan
// item 108), site/index.html is its placeholder.
package webassets

import (
	"embed"
	"io/fs"
)

// placeholders are the placeholder pages: without all:, go:embed would leave
// out the files and directories whose names start with _ or ., such as the
// _astro directory of the site's build output.
//
//go:embed all:app all:site
var placeholders embed.FS

// App returns the tool's files, with its entry page, index.html, at the root:
// the bundle, or the placeholder page in a binary built without it.
func App() fs.FS {
	return app
}

// AppBundled reports whether the binary embeds the tool's bundle, rather than
// the placeholder page.
func AppBundled() bool {
	return appBundled
}

// Site returns the public site's files, with its home page, index.html, at
// the root.
func Site() fs.FS {
	return sub(placeholders, "site")
}

func sub(fsys fs.FS, dir string) fs.FS {
	f, err := fs.Sub(fsys, dir)
	if err != nil {
		// fs.Sub fails only on an invalid path, which dir is not.
		panic(err)
	}
	return f
}
