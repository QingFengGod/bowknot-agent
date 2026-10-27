package app

import (
	"context"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"github.com/bowknot/bowknot-agent/internal/capability"
)

// capabilityService wires the capability API to the host platform.
func (a *App) capabilityService() *capability.Service {
	return &capability.Service{
		AppDataDir: a.appDataDir,
		Identify: func(ctx context.Context) (string, bool) {
			return a.host.AppIDForWindow(mygo.CallerWindow(ctx))
		},
		Platform: capability.Platform{
			PickFile:      a.pickFile,
			PickDirectory: a.pickDirectory,
			ReadClipboard: readClipboardText,
			WriteClipboard: func(text string) error {
				return mygo.Clipboard.Write(transfer.TextData(text))
			},
			Notify: func(title, body string) error {
				return mygo.NewNotification(mygo.NotificationOptions{
					Title:  title,
					Body:   body,
					Group:  Title,
					Silent: false,
				}).Show()
			},
			SupportsNotify: mygo.NotificationsSupported,
			OpenExternal:   mygo.Shell.OpenExternal,
		},
	}
}

func (a *App) pickFile(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:       "选择文件",
		ButtonLabel: "选择",
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

func (a *App) pickDirectory(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:       "选择目录",
		ButtonLabel: "选择",
		Directory:   true,
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

func readClipboardText() (string, error) {
	data, err := mygo.Clipboard.Read(transfer.Text)
	if err != nil {
		return "", err
	}
	raw, err := data.Read(transfer.Text)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// bindCapabilities exposes the capability service to app pages. It runs once
// the app is ready, before any app window exists.
func (a *App) bindCapabilities() {
	a.capabilities = a.capabilityService()
	mygo.BindAs("BowknotCapability", a.capabilities)
}

// forgetAppGrants drops the file grants of an app whose window closed.
func (a *App) forgetAppGrants(id string) {
	if a.capabilities != nil {
		a.capabilities.ForgetWindow(id)
	}
}
