//go:build !darwin

package platform

// UsesFullScreenZoom reports whether the window's green button toggles full
// screen instead of zooming (maximizing) the window. Only macOS does; the
// other platforms maximize and restore.
func UsesFullScreenZoom() bool { return false }
