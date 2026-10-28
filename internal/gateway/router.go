package gateway

// Target is a resolved route target ready to call.
type Target struct {
	Provider   Provider
	Model      string
	Capability Capability
}

// TargetView is a route target the settings page shows and edits.
type TargetView struct {
	ProviderID   string
	ProviderName string
	Model        string
}

// Label returns the text of the target in a select or a chip.
func (t TargetView) Label() string {
	return t.ProviderName + " · " + t.Model
}

// RouteKey identifies the target in a select.
func (t TargetView) RouteKey() string {
	return t.ProviderID + "|" + t.Model
}

// ParseRouteKey returns the provider and model of a select value.
func ParseRouteKey(key string) (providerID, model string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			if i == 0 || i == len(key)-1 {
				return "", "", false
			}
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// resolveTargets returns the enabled, supported targets of a capability in
// route order. It reads a config the caller owns.
func resolveTargets(cfg *Config, registry *AdapterRegistry, cap Capability) []Target {
	routes := cfg.Routes[cap]
	targets := make([]Target, 0, len(routes))
	for _, r := range routes {
		i, ok := cfg.ProviderByID(r.Provider)
		if !ok {
			continue
		}
		p := cfg.Providers[i]
		if !p.Enabled {
			continue
		}
		if _, ok := registry.Get(p.Type); !ok {
			continue
		}
		if !p.HasModel(r.Model) {
			continue
		}
		targets = append(targets, Target{Provider: p, Model: r.Model, Capability: cap})
	}
	return targets
}

// fillDefaultRoutes gives chat and embedding their primary target when they
// are empty, so that the first saved provider is immediately usable.
func fillDefaultRoutes(cfg *Config) {
	p, ok := firstEnabledProvider(cfg)
	if !ok || len(p.Models) == 0 {
		return
	}
	for _, cap := range EditableCapabilities {
		if len(cfg.Routes[cap]) == 0 {
			cfg.Routes[cap] = []Route{{Provider: p.ID, Model: p.Models[0].ID}}
		}
	}
}

func firstEnabledProvider(cfg *Config) (Provider, bool) {
	for _, p := range cfg.Providers {
		if p.Enabled {
			return p, true
		}
	}
	return Provider{}, false
}

func hasEnabledProvider(cfg *Config) bool {
	_, ok := firstEnabledProvider(cfg)
	return ok
}

// targetOptions lists every enabled provider model as a selectable target.
func targetOptions(cfg *Config) []TargetView {
	var out []TargetView
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		for _, m := range p.Models {
			out = append(out, TargetView{ProviderID: p.ID, ProviderName: p.Name, Model: m.ID})
		}
	}
	return out
}

// routeViews returns the configured routes of a capability as views.
func routeViews(cfg *Config, cap Capability) []TargetView {
	routes := cfg.Routes[cap]
	out := make([]TargetView, 0, len(routes))
	for _, r := range routes {
		name := r.Provider
		if i, ok := cfg.ProviderByID(r.Provider); ok {
			name = cfg.Providers[i].Name
		}
		out = append(out, TargetView{ProviderID: r.Provider, ProviderName: name, Model: r.Model})
	}
	return out
}
