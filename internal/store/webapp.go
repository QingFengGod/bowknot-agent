package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/platform"
	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

// CrashThreshold is how many times one app window may crash before it stops
// reloading and shows the crash overlay instead.
const CrashThreshold = 3

// WebAppHost opens one window per installed app, at most one per app at a
// time, and keeps the windows in step with the shell theme.
type WebAppHost struct {
	// AppsDir is {DataDir}/apps.
	AppsDir string
	// ShellVersion gates manifests that need a newer shell.
	ShellVersion string
	// PreloadScript defines window.bowknot in every app page.
	PreloadScript string

	// Repaint repaints the main window, as when an app opens or closes and
	// the launcher's last-opened order changes.
	Repaint func()
	// OnOpened and OnClosed let the app record the running status. They are
	// called after the window exists and after it closed.
	OnOpened func(id string)
	OnClosed func(id string)

	mu      sync.Mutex
	theme   ui.Mode
	windows map[string]*webAppWindow
}

// NewWebAppHost creates the host of the app windows.
func NewWebAppHost(appsDir, shellVersion, preloadScript string, theme ui.Mode) *WebAppHost {
	return &WebAppHost{
		AppsDir:       appsDir,
		ShellVersion:  shellVersion,
		PreloadScript: preloadScript,
		theme:         theme,
		windows:       make(map[string]*webAppWindow),
	}
}

// Open shows the window of an app, or focuses it when it is already open.
func (h *WebAppHost) Open(id string) error {
	if err := validateAppID(id); err != nil {
		return err
	}
	if w := h.window(id); w != nil {
		w.win.Show()
		w.win.Focus()
		return nil
	}
	manifest, err := ReadManifest(filepath.Join(h.AppsDir, id), h.ShellVersion)
	if err != nil {
		return err
	}
	v := &webAppView{
		title:   manifest.Name,
		theme:   h.themeValue(),
		state:   webAppLoading,
		focused: true,
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:             manifest.Name,
		Width:             manifest.Window.Width,
		Height:            manifest.Window.Height,
		MinWidth:          manifest.Window.MinWidth,
		MinHeight:         manifest.Window.MinHeight,
		StateKey:          "webapp-" + id,
		TitleBarStyle:     mygo.TitleBarHidden,
		HideWindowButtons: true,
		BackgroundColor:   ui.BackgroundHex(v.theme),
		Content:           mygoui.View(v.View()),
	})
	w := &webAppWindow{id: id, win: win, view: v, closed: make(chan struct{})}
	v.onMinimize = func() { win.Minimize() }
	v.onMaximize = func() { toggleMaximize(win) }
	v.onClose = func() { win.Close() }
	v.onRetry = func() { w.retry() }
	win.OnClosed(func() { h.windowClosed(w) })

	web, err := win.NewWebView(mygo.WebViewOptions{
		URL:             AppURL(id, manifest.Entry),
		Page:            mygo.PageOptions{PreloadScript: h.PreloadScript},
		BackgroundColor: ui.BackgroundHex(v.theme),
	})
	if err != nil {
		win.Close()
		return fmt.Errorf("store: create web view: %w", err)
	}
	w.web = web
	v.setWeb(web)
	win.OnFocus(func() { v.setFocused(true); win.Invalidate() })
	win.OnBlur(func() { v.setFocused(false); win.Invalidate() })
	web.Page().OnDidFinishLoad(func() { w.loaded() })
	web.Page().OnDidFailLoad(func(e *mygo.LoadError) { w.failed(e) })
	web.Page().OnRenderProcessGone(func(string) { w.crashed() })

	h.mu.Lock()
	if existing, ok := h.windows[id]; ok {
		h.mu.Unlock()
		win.Close()
		existing.win.Show()
		existing.win.Focus()
		return nil
	}
	h.windows[id] = w
	w.registered = true
	h.mu.Unlock()

	if h.OnOpened != nil {
		h.OnOpened(id)
	}
	h.repaint()
	win.Invalidate()
	return nil
}

// Close closes the window of an app and reports whether it was open.
func (h *WebAppHost) Close(id string) bool {
	w := h.window(id)
	if w == nil {
		return false
	}
	w.win.Close()
	return true
}

// WaitClosed waits until the app window closed, or the context is done.
func (h *WebAppHost) WaitClosed(ctx context.Context, id string) error {
	w := h.window(id)
	if w == nil {
		return nil
	}
	select {
	case <-w.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// IsOpen reports whether the app has a window.
func (h *WebAppHost) IsOpen(id string) bool {
	return h.window(id) != nil
}

// AppIDForWindow resolves the app that owns a window. Capability calls use it
// to identify the caller instead of trusting a page-supplied ID.
func (h *WebAppHost) AppIDForWindow(win *mygo.Window) (string, bool) {
	if win == nil {
		return "", false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, w := range h.windows {
		if w.win.ID() == win.ID() {
			return id, true
		}
	}
	return "", false
}

// SetTheme updates the appearance of every app window.
func (h *WebAppHost) SetTheme(mode ui.Mode) {
	h.mu.Lock()
	h.theme = mode
	list := make([]*webAppWindow, 0, len(h.windows))
	for _, w := range h.windows {
		list = append(list, w)
	}
	h.mu.Unlock()
	for _, w := range list {
		w.view.setTheme(mode)
		if w.web != nil {
			_ = w.web.SetBackgroundColor(ui.BackgroundHex(mode))
		}
		_ = w.win.SetBackgroundColor(ui.BackgroundHex(mode))
		w.win.Invalidate()
	}
}

func (h *WebAppHost) window(id string) *webAppWindow {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.windows[id]
}

func (h *WebAppHost) themeValue() ui.Mode {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.theme
}

func (h *WebAppHost) repaint() {
	if h.Repaint != nil {
		h.Repaint()
	}
}

// windowClosed forgets a window that closed, unless it was the duplicate of
// an app that was already open.
func (h *WebAppHost) windowClosed(w *webAppWindow) {
	h.mu.Lock()
	if !w.registered {
		h.mu.Unlock()
		return
	}
	if current, ok := h.windows[w.id]; ok && current == w {
		delete(h.windows, w.id)
	}
	h.mu.Unlock()
	w.remove()
	if h.OnClosed != nil {
		h.OnClosed(w.id)
	}
	h.repaint()
}

func validateAppID(id string) error {
	if !appIDPattern.MatchString(id) {
		return fmt.Errorf("store: invalid app id %q", id)
	}
	return nil
}

func toggleMaximize(win *mygo.Window) {
	if platform.UsesFullScreenZoom() {
		win.ToggleFullScreen()
		return
	}
	if win.IsMaximized() {
		win.Unmaximize()
	} else {
		win.Maximize()
	}
}

// webAppWindow is one open app window and its crash accounting.
type webAppWindow struct {
	id   string
	win  *mygo.Window
	web  *mygo.WebView
	view *webAppView

	mu        sync.Mutex
	crashes   int
	closed    chan struct{}
	closeOnce sync.Once
	// registered is set once the window is in the host map; a duplicate
	// window closed before that has nothing to forget.
	registered bool
}

// loaded clears the overlay and the crash count: a page that finished loading
// and later crashes starts over.
func (w *webAppWindow) loaded() {
	w.mu.Lock()
	w.crashes = 0
	w.mu.Unlock()
	w.view.setState(webAppReady, "")
	w.win.Invalidate()
}

func (w *webAppWindow) failed(e *mygo.LoadError) {
	if w.web != nil && w.web.IsDestroyed() {
		return
	}
	detail := "页面加载失败"
	if e != nil {
		if e.Description != "" {
			detail = fmt.Sprintf("%s（错误码 %d）", e.Description, e.Code)
		} else {
			detail = fmt.Sprintf("页面加载失败（错误码 %d）", e.Code)
		}
	}
	w.view.setState(webAppFailed, detail)
	w.win.Invalidate()
}

// crashed reloads the page until the threshold, then shows the crash overlay.
// It never changes the app status: the user disables or reinstalls the app.
func (w *webAppWindow) crashed() {
	if w.web != nil && w.web.IsDestroyed() {
		return
	}
	w.mu.Lock()
	w.crashes++
	crashes := w.crashes
	w.mu.Unlock()
	if crashes >= CrashThreshold {
		w.view.setState(webAppCrashed,
			fmt.Sprintf("应用已连续崩溃 %d 次，建议关闭窗口并在应用页卸载后重新安装。", crashes))
		w.win.Invalidate()
		return
	}
	w.view.setState(webAppLoading, "应用已崩溃，正在重新加载…")
	w.win.Invalidate()
	w.web.Page().Reload()
}

// retry restarts the page from the error or crash overlay.
func (w *webAppWindow) retry() {
	w.mu.Lock()
	w.crashes = 0
	w.mu.Unlock()
	w.view.setState(webAppLoading, "")
	w.win.Invalidate()
	w.web.Page().Reload()
}

// remove forgets the window once it closed.
func (w *webAppWindow) remove() {
	w.closeOnce.Do(func() { close(w.closed) })
}

// webAppState is what the app window shows above or instead of its page.
type webAppState int

const (
	webAppLoading webAppState = iota
	webAppReady
	webAppFailed
	webAppCrashed
)

// webAppView is the content of an app window: its title bar, the page and the
// loading, error or crash overlay.
type webAppView struct {
	mu      sync.Mutex
	title   string
	theme   ui.Mode
	state   webAppState
	detail  string
	focused bool
	web     *mygo.WebView

	onMinimize func()
	onMaximize func()
	onClose    func()
	onRetry    func()
}

func (v *webAppView) setState(state webAppState, detail string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state, v.detail = state, detail
}

func (v *webAppView) setFocused(focused bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.focused = focused
}

func (v *webAppView) setTheme(mode ui.Mode) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.theme = mode
}

func (v *webAppView) setWeb(web *mygo.WebView) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.web = web
}

func (v *webAppView) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		v.mu.Lock()
		title, theme, state, detail := v.title, v.theme, v.state, v.detail
		focused, web := v.focused, v.web
		onMinimize, onMaximize := v.onMinimize, v.onMaximize
		onClose, onRetry := v.onClose, v.onRetry
		v.mu.Unlock()

		t := ui.Theme(theme)
		c.SetTheme(t)
		mygoui.Column(c).Fill().Background(t.Background).Children(func() {
			ui.WindowTitleBar(c, ui.WindowTitleBarActions{
				Title:      title,
				Focused:    focused,
				OnMinimize: onMinimize,
				OnMaximize: onMaximize,
				OnClose:    onClose,
			})
			body := mygoui.Column(c).Grow(1).FillWidth()
			body.Children(func() {
				if web != nil {
					mygoui.WebView(c, web).Grow(1)
				} else {
					mygoui.Spacer(c)
				}
				switch state {
				case webAppLoading:
					loadingOverlay(c, t, detail)
				case webAppFailed:
					statusOverlay(c, t, "应用加载失败", detail, "重新加载", onRetry, onClose)
				case webAppCrashed:
					statusOverlay(c, t, "应用无响应", detail, "重新加载", onRetry, onClose)
				}
			})
		})
	}
}

func loadingOverlay(c *mygoui.Context, t *mygoui.Theme, detail string) {
	box := mygoui.Box(c).Absolute().Fill().Center().Background(t.Background)
	box.Children(func() {
		column := mygoui.Column(c).Gap(6).AlignItems(mygoui.Center)
		column.Children(func() {
			mygoui.Text(c, "正在加载应用…").FontSize(14).TextColor(t.Text)
			if detail != "" {
				mygoui.Text(c, detail).FontSize(12).TextColor(t.TextMuted)
			}
		})
	})
}

func statusOverlay(c *mygoui.Context, t *mygoui.Theme, title, detail, retryLabel string, onRetry, onClose func()) {
	box := mygoui.Box(c).Absolute().Fill().Center().Background(t.Background)
	box.Children(func() {
		column := mygoui.Column(c).Gap(8).AlignItems(mygoui.Center)
		column.Children(func() {
			mygoui.Text(c, title).FontSize(16).Bold().TextColor(t.Text)
			mygoui.Text(c, detail).FontSize(12).TextColor(t.TextMuted)
			row := mygoui.Row(c).Gap(8)
			row.Children(func() {
				retry := mygoui.ButtonBase(c).Label(retryLabel).Shrink(0).
					Padding(6, 14).Radius(t.Radius).Background(t.Accent)
				retry.Children(func() {
					mygoui.Text(c, retryLabel).FontSize(12).TextColor(t.AccentText)
				})
				if retry.Clicked() && onRetry != nil {
					onRetry()
				}
				closeButton := mygoui.ButtonBase(c).Label("关闭窗口").Shrink(0).
					Padding(6, 14).Radius(t.Radius).Background(t.SurfacePressed)
				closeButton.Children(func() {
					mygoui.Text(c, "关闭窗口").FontSize(12).TextColor(t.Text)
				})
				if closeButton.Clicked() && onClose != nil {
					onClose()
				}
			})
		})
	})
}

// CloseAll closes every app window, as the app quits.
func (h *WebAppHost) CloseAll() {
	h.mu.Lock()
	list := make([]*webAppWindow, 0, len(h.windows))
	for _, w := range h.windows {
		list = append(list, w)
	}
	h.mu.Unlock()
	for _, w := range list {
		w.win.Close()
	}
}
