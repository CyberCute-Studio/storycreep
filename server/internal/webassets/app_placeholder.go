//go:build !webdist

package webassets

// A binary built without the webdist tag serves the placeholder page.
var (
	app        = sub(placeholders, "app")
	appBundled = false
)
