package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://api.openai.com/v1/", "https://api.openai.com/v1", true},
		{"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1", true},
		{"https://host", "", false},
		{"ftp://host/v1", "", false},
		{"https://user:pass@host/v1", "", false},
		{"https://host/v1?x=1", "", false},
		{"https://host/v1#frag", "", false},
		{"not a url", "", false},
	}
	for _, tc := range tests {
		got, err := NormalizeBaseURL(tc.in)
		if tc.ok && err != nil {
			t.Errorf("NormalizeBaseURL(%q) error = %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("NormalizeBaseURL(%q) = %q, want an error", tc.in, got)
		}
		if tc.ok && got != tc.want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestConfigNormalizeDropsInvalidRoutes(t *testing.T) {
	cfg := &Config{
		Schema: ConfigSchema,
		Providers: []Provider{
			{ID: "p-00000001", Name: " A ", Type: ProviderOpenAI,
				BaseURL: "https://a.example/v1/", Enabled: true,
				Models: []string{" m1 ", "m2", "m1", ""}},
			{ID: "p-00000001", Name: "dup", Type: ProviderOpenAI, BaseURL: "https://b.example/v1"},
		},
		Routes: map[Capability][]Route{
			CapabilityChat: {
				{Provider: "p-00000001", Model: "m1"},
				{Provider: "p-00000001", Model: "missing"},
				{Provider: "p-deadbeef", Model: "m1"},
				{Provider: "p-00000001", Model: "m1"},
			},
			"unknown": {{Provider: "p-00000001", Model: "m1"}},
		},
	}
	cfg.Normalize()
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	p := cfg.Providers[0]
	if p.Name != "A" || p.BaseURL != "https://a.example/v1" {
		t.Errorf("provider = %+v, want trimmed name and base URL", p)
	}
	if got := strings.Join(p.Models, ","); got != "m1,m2" {
		t.Errorf("models = %q, want m1,m2", got)
	}
	if len(cfg.Routes[CapabilityChat]) != 1 || cfg.Routes[CapabilityChat][0].Model != "m1" {
		t.Errorf("chat routes = %+v, want only m1", cfg.Routes[CapabilityChat])
	}
	if _, ok := cfg.Routes["unknown"]; ok {
		t.Error("unknown route key kept")
	}
	if _, ok := cfg.Routes[CapabilityEmbedding]; !ok {
		t.Error("known route key missing after normalize")
	}
}

func TestConfigValidate(t *testing.T) {
	valid := Provider{ID: "p-00000001", Name: "OpenAI", Type: ProviderOpenAI,
		BaseURL: "https://api.example/v1", Enabled: true, Models: []string{"m"}}
	cfg := &Config{Schema: ConfigSchema, Providers: []Provider{valid},
		Routes: map[Capability][]Route{}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	tests := []struct {
		name     string
		provider Provider
		field    string
	}{
		{"empty name", withName(valid, "  "), "name"},
		{"long name", withName(valid, strings.Repeat("a", 65)), "name"},
		{"bad url", withURL(valid, "https://host"), "base_url"},
		{"empty type", withType(valid, ""), "type"},
		{"long model", withModels(valid, strings.Repeat("m", 129)), "models"},
	}
	for _, tc := range tests {
		cfg := &Config{Schema: ConfigSchema, Providers: []Provider{tc.provider},
			Routes: map[Capability][]Route{}}
		err := cfg.Validate()
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		verr, ok := err.(*ValidationError)
		if !ok {
			t.Errorf("%s: error is %T, want *ValidationError", tc.name, err)
			continue
		}
		found := false
		for _, fe := range verr.Errors {
			if fe.Field == tc.field {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: errors = %+v, want field %s", tc.name, verr.Errors, tc.field)
		}
	}
}

func withName(p Provider, name string) Provider { p.Name = name; return p }
func withURL(p Provider, url string) Provider   { p.BaseURL = url; return p }
func withType(p Provider, typ string) Provider  { p.Type = typ; return p }

func withModels(p Provider, models ...string) Provider {
	p.Models = models
	return p
}

func TestConfigLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", ProvidersFileName)
	if cfg, err := LoadConfig(path); err != nil || len(cfg.Providers) != 0 {
		t.Fatalf("missing config = (%+v, %v), want empty and nil", cfg, err)
	}
	cfg := NewConfig()
	cfg.Providers = []Provider{{ID: "p-00000001", Name: "Local", Type: ProviderOpenAI,
		BaseURL: "http://127.0.0.1:1234/v1", Enabled: true, Models: []string{"m"}}}
	cfg.Routes[CapabilityChat] = []Route{{Provider: "p-00000001", Model: "m"}}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 1 || loaded.Providers[0].Name != "Local" {
		t.Errorf("loaded = %+v", loaded.Providers)
	}
	if len(loaded.Routes[CapabilityChat]) != 1 {
		t.Errorf("loaded routes = %+v", loaded.Routes)
	}
}

func TestConfigLoadRejectsUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProvidersFileName)
	if err := os.WriteFile(path, []byte(`{"schema":99,"providers":[{"id":"p-00000001"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err == nil {
		t.Error("unknown schema loaded without an error")
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("unknown schema kept %d providers, want none", len(cfg.Providers))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"schema":99`) {
		t.Error("load rewrote the damaged file")
	}
}

func TestConfigLoadRejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProvidersFileName)
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Error("malformed config loaded without an error")
	}
}
