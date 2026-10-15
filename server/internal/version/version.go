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

// commit and commitTime describe the revision of a build whose go tool
// records none, as the container image's build does, which has no Git
// checkout to read it from (deploy/Dockerfile). Such a build sets them as it
// sets release:
//
//	-X github.com/CyberCute-Studio/storycreep/server/internal/version.commit=<revision>
//	-X github.com/CyberCute-Studio/storycreep/server/internal/version.commitTime=<RFC 3339 time>
//
// A revision that the go tool records wins over them.
var commit, commitTime string

// Info describes a build.
type Info struct {
	// Version is the release version, or for any other build the module
	// version the go tool recorded, which is "(devel)" for a build of the
	// module's own source, as from a checkout or in the container image.
	Version string
	// Commit is the revision of the source the binary was built from, or ""
	// if neither the go tool nor the build recorded it, as in a build by go
	// test. A build from a checkout with uncommitted changes adds "-dirty".
	Commit string
	// CommitTime is the time of that revision, in RFC 3339, or "".
	CommitTime string
	// GoVersion is the version of Go the binary was built with.
	GoVersion string
}

// Get returns the build of the running binary.
var Get = sync.OnceValue(func() Info {
	// ReadBuildInfo returns nil when the binary has no build information.
	bi, _ := debug.ReadBuildInfo()
	return describe(stamp{release: release, commit: commit, commitTime: commitTime}, bi)
})

// stamp is what a build sets with -ldflags -X.
type stamp struct {
	release, commit, commitTime string
}

// describe returns the build that s and bi describe. bi is nil if the binary
// has no build information.
func describe(s stamp, bi *debug.BuildInfo) Info {
	info := Info{Version: s.release, GoVersion: runtime.Version()}
	dirty := false
	if bi != nil {
		if info.Version == "" {
			info.Version = bi.Main.Version
		}
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				info.Commit = setting.Value
			case "vcs.time":
				info.CommitTime = setting.Value
			case "vcs.modified":
				dirty = setting.Value == "true"
			}
		}
	}
	switch {
	case info.Version != "":
	case bi == nil:
		info.Version = "(unknown)"
	default:
		info.Version = "(devel)"
	}
	if info.Commit == "" {
		info.Commit, info.CommitTime = s.commit, s.commitTime
	}
	if dirty && info.Commit != "" {
		info.Commit += "-dirty"
	}
	return info
}
