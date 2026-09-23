package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	info := Get()

	if info.Version == "" {
		t.Errorf("expected non-empty Version, got empty string")
	}

	if info.GoVersion != runtime.Version() {
		t.Errorf("expected GoVersion %q, got %q", runtime.Version(), info.GoVersion)
	}

	if !strings.Contains(info.Platform, runtime.GOOS) {
		t.Errorf("expected Platform to contain %q, got %q", runtime.GOOS, info.Platform)
	}

	str := info.String()
	if !strings.Contains(str, "CloudX") {
		t.Errorf("expected String() to contain 'CloudX', got %q", str)
	}
}
