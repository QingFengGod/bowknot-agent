//go:build darwin

package platform

// UsesFullScreenZoom reports whether the window's green button toggles full
// screen instead of zooming (maximizing) the window. macOS does.
func UsesFullScreenZoom() bool { return true }
