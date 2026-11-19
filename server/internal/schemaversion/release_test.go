//go:build !synthetic_schema

package schemaversion_test

import (
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
)

// TestReleaseRange checks that a build without the synthetic_schema build
// tag, as every release is, reads exactly the real versions of the published
// format, those whose schemas /schema/published holds, from the oldest to the
// newest, which package published's types are of, and none between them that
// it lacks (plan item 77), as a release does while no version's support
// window has ended (PRD §6.4); a release that stops reading one changes what
// this test takes. The image's test checks that the release's /version
// declares them (package deploy).
func TestReleaseRange(t *testing.T) {
	real := realVersions(t)
	want := schemaversion.Range{Min: real[0], Max: real[len(real)-1]}
	if got := schemaversion.Supported(); got != want {
		t.Errorf("a release reads versions %s, and /schema/published holds %s", got, want)
	}
	if want.Max != published.SchemaVersion {
		t.Errorf("the newest version that /schema/published holds is %d, and package published's types are of %d",
			want.Max, published.SchemaVersion)
	}
	if len(real) != want.Max-want.Min+1 {
		t.Errorf("/schema/published holds the versions %v, which skip one", real)
	}
}
