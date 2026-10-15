package version

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func TestGet(t *testing.T) {
	info := Get()
	if info.Version == "" || info.GoVersion != runtime.Version() {
		t.Errorf("Get() = %+v, want a version and %s", info, runtime.Version())
	}
	if Get() != info {
		t.Errorf("Get() changed between calls")
	}
}

// TestDescribe checks where each field comes from: the stamp that -ldflags
// sets, or the build information that the go tool records.
func TestDescribe(t *testing.T) {
	checkout := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "abc123"},
		{Key: "vcs.time", Value: "2026-10-15T12:00:00Z"},
	}}
	dirty := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "abc123"},
		{Key: "vcs.modified", Value: "true"},
	}}
	noVCS := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	image := stamp{release: "v1.2.0", commit: "def456", commitTime: "2026-10-14T08:00:00Z"}

	for _, tc := range []struct {
		name  string
		stamp stamp
		bi    *debug.BuildInfo
		want  Info
	}{
		{"checkout", stamp{}, checkout,
			Info{Version: "(devel)", Commit: "abc123", CommitTime: "2026-10-15T12:00:00Z"}},
		{"dirty checkout", stamp{}, dirty, Info{Version: "(devel)", Commit: "abc123-dirty"}},
		{"release from a checkout", stamp{release: "v1.2.0"}, checkout,
			Info{Version: "v1.2.0", Commit: "abc123", CommitTime: "2026-10-15T12:00:00Z"}},
		{"image build", image, noVCS,
			Info{Version: "v1.2.0", Commit: "def456", CommitTime: "2026-10-14T08:00:00Z"}},
		{"image build of the main branch", stamp{commit: "def456"}, noVCS,
			Info{Version: "(devel)", Commit: "def456"}},
		{"the go tool's revision wins", image, checkout,
			Info{Version: "v1.2.0", Commit: "abc123", CommitTime: "2026-10-15T12:00:00Z"}},
		{"no build information", stamp{}, nil, Info{Version: "(unknown)"}},
		{"no build information, stamped", image, nil,
			Info{Version: "v1.2.0", Commit: "def456", CommitTime: "2026-10-14T08:00:00Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.want.GoVersion = runtime.Version()
			if got := describe(tc.stamp, tc.bi); got != tc.want {
				t.Errorf("describe() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
