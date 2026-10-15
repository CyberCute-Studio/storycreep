// Package webassets holds the static files of the web interface, embedded in
// the binary, so that the server serves them without a Node process and the
// container image needs no other files (PRD §10.6, §10.8, FR-PUB-01 AC3): the
// tool's bundle, and the public site's.
//
// Until the tool (plan item 53) and the site (plan item 108) are built, each
// is one placeholder page, app/index.html and site/index.html. Those items
// replace them with their build output.
package webassets

import (
	"embed"
	"io/fs"
)

// files are the two directories, with every file in them: without all:,
// go:embed would leave out the files and directories whose names start with
// _ or ., such as the _astro directory of the site's build output.
//
//go:embed all:app all:site
var files embed.FS

// App returns the tool's files, with its entry page, index.html, at the root.
func App() fs.FS {
	return sub("app")
}

// Site returns the public site's files, with its home page, index.html, at
// the root.
func Site() fs.FS {
	return sub("site")
}

func sub(dir string) fs.FS {
	f, err := fs.Sub(files, dir)
	if err != nil {
		// fs.Sub fails only on an invalid path, which dir is not.
		panic(err)
	}
	return f
}
