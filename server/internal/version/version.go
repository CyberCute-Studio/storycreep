// Package version describes the build of the running binary, for /version
// and for the logs (PRD FR-ADMIN-05).
package version

import (
	"runtime"
	"runtime/debug"
	"sync"
)

// release is the release version, such as "v1.2.0". A release build sets it
// with
//
//	go build -ldflags "-X github.com/CyberCute-Studio/storycreep/server/internal/version.release=v1.2.0"
//
// Other builds leave it empty, and Get reports the module version instead.
var release string

// Info describes a build.
type Info struct {
	// Version is the release version, or for any other build the module
	// version the go tool recorded, which is "(devel)" for a build from a
	// checkout.
	Version string
	// Commit is the revision of the source the binary was built from, or ""
	// if the go tool did not record it, as in a build outside a checkout or
	// by go test. A build from a checkout with uncommitted changes adds
	// "-dirty".
	Commit string
	// CommitTime is the time of that revision, in RFC 3339, or "".
	CommitTime string
	// GoVersion is the version of Go the binary was built with.
	GoVersion string
}

// Get returns the build of the running binary.
var Get = sync.OnceValue(func() Info {
	info := Info{Version: release, GoVersion: runtime.Version()}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		if info.Version == "" {
			info.Version = "(unknown)"
		}
		return info
	}
	if info.Version == "" {
		info.Version = bi.Main.Version
	}
	if info.Version == "" {
		info.Version = "(devel)"
	}
	dirty := false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.time":
			info.CommitTime = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty && info.Commit != "" {
		info.Commit += "-dirty"
	}
	return info
})
