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
				Models: []Model{{ID: " m1 ", ContextWindow: 128000},
					{ID: "m2"}, {ID: "m1"}, {ID: ""}}},
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
	if got := modelIDs(p.Models); got != "m1,m2" {
		t.Errorf("models = %q, want m1,m2", got)
	}
	if p.Models[0].ContextWindow != 128000 {
		t.Errorf("context window = %d, want 128000", p.Models[0].ContextWindow)
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
		BaseURL: "https://api.example/v1", Enabled: true, Models: []Model{{ID: "m"}}}
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
		{"negative context", withContext(valid, -1), "models"},
		{"huge output", withOutput(valid, MaxModelTokens+1), "models"},
		{"output over context", withLimits(valid, 1000, 2000), "models"},
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

func withModels(p Provider, ids ...string) Provider {
	models := make([]Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, Model{ID: id})
	}
	p.Models = models
	return p
}

func withContext(p Provider, context int) Provider {
	p.Models[0].ContextWindow = context
	return p
}

func withOutput(p Provider, output int) Provider {
	p.Models[0].MaxOutputTokens = output
	return p
}

func withLimits(p Provider, context, output int) Provider {
	p.Models[0].ContextWindow = context
	p.Models[0].MaxOutputTokens = output
	return p
}

func modelIDs(models []Model) string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return strings.Join(ids, ",")
}

func TestConfigLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", ProvidersFileName)
	if cfg, err := LoadConfig(path); err != nil || len(cfg.Providers) != 0 {
		t.Fatalf("missing config = (%+v, %v), want empty and nil", cfg, err)
	}
	cfg := NewConfig()
	cfg.Providers = []Provider{{ID: "p-00000001", Name: "Local", Type: ProviderOpenAI,
		BaseURL: "http://127.0.0.1:1234/v1", Enabled: true,
		Models: []Model{{ID: "m", ContextWindow: 8192, MaxOutputTokens: 2048,
			SupportsImages: true}}}}
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
	m := loaded.Providers[0].Models[0]
	if m.ContextWindow != 8192 || m.MaxOutputTokens != 2048 || !m.SupportsImages {
		t.Errorf("model metadata = %+v", m)
	}
}

func TestSaveConfigOverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProvidersFileName)
	first := NewConfig()
	first.Providers = []Provider{{ID: "p-00000001", Name: "First", Type: ProviderOpenAI,
		BaseURL: "https://a.example/v1", Enabled: true, Models: []Model{{ID: "m1"}}}}
	if err := SaveConfig(path, first); err != nil {
		t.Fatal(err)
	}
	second := first.Clone()
	second.Providers[0].Name = "Second"
	if err := SaveConfig(path, second); err != nil {
		t.Fatalf("second save = %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 1 || loaded.Providers[0].Name != "Second" {
		t.Errorf("loaded = %+v, want the second config", loaded.Providers)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file left behind: %v", err)
	}
}

func TestSaveConfigReadOnlyDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ProvidersFileName)
	if err := os.WriteFile(path, []byte("{}"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(path, NewConfig()); err != nil {
		t.Fatalf("save over a read-only file = %v, want success", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 0 {
		t.Errorf("loaded = %+v", loaded.Providers)
	}
}

func TestConfigLoadRejectsOldSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProvidersFileName)
	raw := `{"schema":1,"providers":[{"id":"p-00000001","name":"Old",
		"type":"openai","base_url":"https://api.example.com/v1","enabled":true,
		"models":["m1"]}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err == nil {
		t.Fatal("old schema loaded without an error")
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("providers = %+v, want an empty config", cfg.Providers)
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
