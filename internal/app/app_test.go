package app

import "testing"

func TestWindowSize(t *testing.T) {
	tests := []struct {
		name          string
		workWidth     int
		workHeight    int
		width, height int
	}{
		{"80 percent", 1920, 1080, 1536, 864},
		{"exact minimum", 1280, 800, 1024, 640},
		{"clamped to minimum", 1000, 700, 960, 640},
		{"smaller than minimum", 800, 500, 800, 500},
		{"no display", 0, 0, 960, 640},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height := windowSize(tt.workWidth, tt.workHeight)
			if width != tt.width || height != tt.height {
				t.Errorf("windowSize(%d, %d) = (%d, %d), want (%d, %d)",
					tt.workWidth, tt.workHeight, width, height, tt.width, tt.height)
			}
		})
	}
}

func TestShouldHideOnClose(t *testing.T) {
	tests := []struct {
		name     string
		quitting bool
		hasTray  bool
		want     bool
	}{
		{"close with tray hides", false, true, true},
		{"close without tray quits", false, false, false},
		{"quit with tray closes", true, true, false},
		{"quit without tray closes", true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldHideOnClose(tt.quitting, tt.hasTray); got != tt.want {
				t.Errorf("shouldHideOnClose(%v, %v) = %v, want %v",
					tt.quitting, tt.hasTray, got, tt.want)
			}
		})
	}
}

func TestMinWindowSize(t *testing.T) {
	tests := []struct {
		name          string
		workWidth     int
		workHeight    int
		width, height int
	}{
		{"roomy work area", 1920, 1080, 960, 640},
		{"small work area", 800, 500, 800, 500},
		{"no display", 0, 0, 960, 640},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height := minWindowSize(tt.workWidth, tt.workHeight)
			if width != tt.width || height != tt.height {
				t.Errorf("minWindowSize(%d, %d) = (%d, %d), want (%d, %d)",
					tt.workWidth, tt.workHeight, width, height, tt.width, tt.height)
			}
		})
	}
}
