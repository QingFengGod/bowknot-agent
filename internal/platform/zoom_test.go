package platform

import (
	"runtime"
	"testing"
)

func TestUsesFullScreenZoom(t *testing.T) {
	want := runtime.GOOS == "darwin"
	if got := UsesFullScreenZoom(); got != want {
		t.Errorf("UsesFullScreenZoom() = %v on %s, want %v", got, runtime.GOOS, want)
	}
}
