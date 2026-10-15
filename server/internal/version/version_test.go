package version

import (
	"runtime"
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
