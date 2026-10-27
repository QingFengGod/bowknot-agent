package app

import (
	"log"

	"github.com/egoist/mygo"

	"github.com/bowknot/bowknot-agent/internal/gateway"
	"github.com/bowknot/bowknot-agent/internal/platform"
	settingspage "github.com/bowknot/bowknot-agent/internal/settings"
	"github.com/bowknot/bowknot-agent/internal/shell"
)

// The app injects a *gateway.Service as the settings Gateway.
var _ settingspage.Gateway = (*gateway.Service)(nil)

// setUpGateway wires the AI gateway and the settings page. A missing gateway
// directory leaves the settings page readable but without model services;
// nothing else in the shell depends on the gateway yet.
func (a *App) setUpGateway() {
	dir, err := platform.GatewayDir()
	if err != nil {
		log.Printf("app: gateway directory: %v; model services are unavailable", err)
		a.settingsPage = settingspage.NewPage(nil)
		a.settingsPage.Repaint = a.invalidateMain
		return
	}
	svc, err := gateway.NewService(gateway.Options{Dir: dir})
	if err != nil {
		log.Printf("app: start gateway: %v; model services are unavailable", err)
		a.settingsPage = settingspage.NewPage(nil)
		a.settingsPage.Repaint = a.invalidateMain
		return
	}
	a.gateway = svc
	a.settingsPage = settingspage.NewPage(svc)
	a.settingsPage.Repaint = a.invalidateMain
	a.applyGatewaySnapshot(svc.Snapshot())
	svc.SetOnSnapshot(func(snap gateway.Snapshot) {
		mygo.RunOnMain(func() {
			a.applyGatewaySnapshot(snap)
			a.invalidateMain()
		})
	})
}

// applyGatewaySnapshot mirrors the gateway status into the sidebar. The
// gateway does not know about the shell, so the mapping lives here.
func (a *App) applyGatewaySnapshot(snap gateway.Snapshot) {
	state := shell.GatewayNotConfigured
	switch snap.State {
	case gateway.StateOnline:
		state = shell.GatewayOnline
	case gateway.StateOffline:
		state = shell.GatewayOffline
	}
	a.shell.Status = shell.Status{Gateway: state, Model: snap.Model}
}
