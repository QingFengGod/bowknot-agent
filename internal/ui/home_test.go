package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestHomePlaceholder(t *testing.T) {
	tt := ui.NewTester(Home("Windows"), 960, 640)
	if !tt.HasText("Bowknot Agent") {
		t.Fatalf("texts %q missing the app name", tt.Texts())
	}
	if !tt.HasText("运行平台：Windows") {
		t.Errorf("texts %q missing the platform line", tt.Texts())
	}
}
