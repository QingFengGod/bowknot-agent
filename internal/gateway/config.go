// Package gateway 提供进程内的 AI 网关：供应商配置、密钥存储、能力路由与调用。
package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// ConfigSchema is the version of providers.json this build reads and writes.
// The format changed when models gained metadata; older files are rejected
// rather than migrated, because nothing has shipped yet.
const ConfigSchema = 2

// MaxModels is the largest number of models kept per provider.
const MaxModels = 100

// MaxModelTokens bounds the context window and max output of a model.
const MaxModelTokens = 10_000_000

// ProviderOpenAI is the only provider type of this build.
const ProviderOpenAI = "openai"

// TestKindModels is the kind of the persisted connection test.
const TestKindModels = "models"

// Capability is a model capability a provider can serve.
type Capability string

// Capabilities of the gateway.
const (
	CapabilityChat      Capability = "chat"
	CapabilityImage     Capability = "image"
	CapabilityVideo     Capability = "video"
	CapabilityTTS       Capability = "tts"
	CapabilityASR       Capability = "asr"
	CapabilityEmbedding Capability = "embedding"
)

// Capabilities are the route keys of the config, in display order.
var Capabilities = []Capability{
	CapabilityChat, CapabilityImage, CapabilityVideo,
	CapabilityTTS, CapabilityASR, CapabilityEmbedding,
}

// EditableCapabilities are the routes the settings page exposes; the rest
// stay read-only until their modules exist.
var EditableCapabilities = []Capability{CapabilityChat, CapabilityEmbedding}

// Route is one ordered target of a capability: the first is the primary.
type Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// TestResult is the outcome of the last connection test of a provider.
type TestResult struct {
	OK      bool   `json:"ok"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	At      int64  `json:"at"`
}

// Model is one model a provider offers, with the metadata agents need to
// plan a request. Zero values mean the limit or the feature is unknown.
type Model struct {
	ID              string `json:"id"`
	ContextWindow   int    `json:"context_window,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	SupportsImages  bool   `json:"supports_images,omitempty"`
	SupportsTools   bool   `json:"supports_tools,omitempty"`
}

// Provider is one configured model service.
type Provider struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Type     string      `json:"type"`
	BaseURL  string      `json:"base_url"`
	Enabled  bool        `json:"enabled"`
	Models   []Model     `json:"models"`
	LastTest *TestResult `json:"last_test,omitempty"`
}

// HasModel reports whether the provider offers a model with the ID.
func (p Provider) HasModel(id string) bool {
	for _, m := range p.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// Config is the content of providers.json.
type Config struct {
	Schema    int                    `json:"schema"`
	Providers []Provider             `json:"providers"`
	Routes    map[Capability][]Route `json:"routes"`
}

// NewConfig returns an empty configuration at the current schema.
func NewConfig() *Config {
	return &Config{
		Schema:    ConfigSchema,
		Providers: []Provider{},
		Routes:    map[Capability][]Route{},
	}
}

// Clone returns a deep copy, so callers can change a config without touching
// the one another goroutine reads.
func (c *Config) Clone() *Config {
	if c == nil {
		return NewConfig()
	}
	out := &Config{Schema: c.Schema, Providers: make([]Provider, len(c.Providers)),
		Routes: make(map[Capability][]Route, len(c.Routes))}
	for i, p := range c.Providers {
		out.Providers[i] = p.Clone()
	}
	for cap, routes := range c.Routes {
		out.Routes[cap] = append([]Route(nil), routes...)
	}
	return out
}

// Clone returns a copy of the provider with its own model slice.
func (p Provider) Clone() Provider {
	p.Models = append([]Model(nil), p.Models...)
	if p.LastTest != nil {
		last := *p.LastTest
		p.LastTest = &last
	}
	return p
}

// ProviderByID returns the index of the provider with id.
func (c *Config) ProviderByID(id string) (int, bool) {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

// FieldError is one validation failure, shown by field in the settings page.
type FieldError struct {
	Field   string
	Message string
}

// ValidationError collects the field errors of a configuration.
type ValidationError struct {
	Errors []FieldError
}

// Error returns the first failure, for logs; the page shows all of them.
func (e *ValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "gateway: invalid configuration"
	}
	return fmt.Sprintf("gateway: %s: %s", e.Errors[0].Field, e.Errors[0].Message)
}

// Normalize trims and deduplicates the configuration in place, drops routes
// that point at missing providers or models, and ignores unknown route keys.
// It never invents providers or models.
func (c *Config) Normalize() {
	if c.Schema == 0 {
		c.Schema = ConfigSchema
	}
	providers := make([]Provider, 0, len(c.Providers))
	seen := make(map[string]bool, len(c.Providers))
	for _, p := range c.Providers {
		p.ID = strings.TrimSpace(p.ID)
		p.Name = strings.TrimSpace(p.Name)
		p.Type = strings.TrimSpace(p.Type)
		p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		p.Models = normalizeModels(p.Models)
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		providers = append(providers, p)
	}
	c.Providers = providers

	routes := make(map[Capability][]Route, len(Capabilities))
	for _, cap := range Capabilities {
		routes[cap] = normalizeRoutes(c, c.Routes[cap])
	}
	c.Routes = routes
}

func normalizeModels(models []Model) []Model {
	out := make([]Model, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" || seen[m.ID] || utf8.RuneCountInString(m.ID) > 128 {
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
		if len(out) == MaxModels {
			break
		}
	}
	return out
}

// modelsFromIDs turns the model IDs of a connection test into models.
func modelsFromIDs(ids []string) []Model {
	models := make([]Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, Model{ID: id})
	}
	return normalizeModels(models)
}

func normalizeRoutes(c *Config, routes []Route) []Route {
	out := make([]Route, 0, len(routes))
	seen := make(map[Route]bool, len(routes))
	for _, r := range routes {
		r.Provider = strings.TrimSpace(r.Provider)
		r.Model = strings.TrimSpace(r.Model)
		if r.Provider == "" || r.Model == "" || seen[r] {
			continue
		}
		i, ok := c.ProviderByID(r.Provider)
		if !ok || !c.Providers[i].HasModel(r.Model) {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// Validate reports the field errors that prevent saving.
func (c *Config) Validate() error {
	var errs []FieldError
	if c.Schema != ConfigSchema {
		errs = append(errs, FieldError{"schema", fmt.Sprintf("配置版本 %d 不受支持", c.Schema)})
	}
	ids := make(map[string]bool, len(c.Providers))
	names := make(map[string]bool, len(c.Providers))
	for _, p := range c.Providers {
		if !strings.HasPrefix(p.ID, "p-") || len(p.ID) != 10 {
			errs = append(errs, FieldError{"providers", "供应商标识无效"})
		}
		if ids[p.ID] {
			errs = append(errs, FieldError{"providers", "供应商标识重复"})
		}
		ids[p.ID] = true

		name := strings.TrimSpace(p.Name)
		n := utf8.RuneCountInString(name)
		if n == 0 || n > 64 {
			errs = append(errs, FieldError{"name", "供应商名称需要 1–64 个字符"})
		} else if names[name] {
			errs = append(errs, FieldError{"name", "供应商名称重复：" + name})
		}
		names[name] = true

		if p.Type == "" {
			errs = append(errs, FieldError{"type", "供应商类型不能为空"})
		}
		if _, err := NormalizeBaseURL(p.BaseURL); err != nil {
			errs = append(errs, FieldError{"base_url", err.Error()})
		}
		if len(p.Models) > MaxModels {
			errs = append(errs, FieldError{"models", fmt.Sprintf("模型最多 %d 条", MaxModels)})
		}
		for _, m := range p.Models {
			if m.ID == "" || utf8.RuneCountInString(m.ID) > 128 {
				errs = append(errs, FieldError{"models", "模型名称需要 1–128 个字符"})
				break
			}
			if err := validateTokens(m.ContextWindow); err != nil {
				errs = append(errs, FieldError{"models", "上下文长度" + err.Error()})
				break
			}
			if err := validateTokens(m.MaxOutputTokens); err != nil {
				errs = append(errs, FieldError{"models", "最大输出" + err.Error()})
				break
			}
			if m.MaxOutputTokens > 0 && m.ContextWindow > 0 &&
				m.MaxOutputTokens > m.ContextWindow {
				errs = append(errs, FieldError{"models", "最大输出不能大于上下文长度"})
				break
			}
		}
	}
	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

func validateTokens(v int) error {
	if v < 0 || v > MaxModelTokens {
		return fmt.Errorf("需为 0–%d 的整数", MaxModelTokens)
	}
	return nil
}

// NormalizeBaseURL trims a provider address to its canonical form: http or
// https, no userinfo, query or fragment, and a non-root API path.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("供应商地址不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("供应商地址无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("供应商地址须以 http 或 https 开头")
	}
	if u.User != nil {
		return "", fmt.Errorf("供应商地址不能包含用户名或密码")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("供应商地址不能包含查询串或片段")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		return "", fmt.Errorf("供应商地址须包含 API 版本路径，例如 /v1")
	}
	u.RawPath = ""
	return u.String(), nil
}

// NewProviderID returns a fresh p-xxxxxxxx identifier.
func NewProviderID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("p-%08x", uint32(time.Now().UnixNano()))
	}
	return "p-" + hex.EncodeToString(b[:])
}

// LoadConfig reads path. A missing file is an empty config; a malformed file
// or an unknown schema also yields an empty config together with an error, so
// that the caller reports it and the damaged file stays untouched.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewConfig(), nil
		}
		return NewConfig(), fmt.Errorf("gateway: read config: %w", err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return NewConfig(), fmt.Errorf("gateway: parse config: %w", err)
	}
	if cfg.Schema != ConfigSchema {
		return NewConfig(), fmt.Errorf("gateway: unsupported config schema %d", cfg.Schema)
	}
	cfg.Normalize()
	return cfg, nil
}

// SaveConfig writes cfg to path through a temporary file, so that a failed
// write never replaces a good config.
func SaveConfig(path string, cfg *Config) error {
	clone := cfg.Clone()
	clone.Normalize()
	data, err := json.MarshalIndent(clone, "", "  ")
	if err != nil {
		return fmt.Errorf("gateway: encode config: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("gateway: create config directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("gateway: write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		if retry := os.Rename(tmp, path); retry != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("gateway: replace config: %w", retry)
		}
	}
	return nil
}
