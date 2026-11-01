//go:build webdist

package webassets

import "embed"

// bundle is the build of web/app, which pnpm --filter @storycreep/app build
// writes to dist/app. A binary built with the webdist tag before that does
// not compile, rather than serve no tool.
//
//go:embed all:dist/app
var bundle embed.FS

var (
	app        = sub(bundle, "dist/app")
	appBundled = true
)
