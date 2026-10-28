package gateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// ProvidersFileName is the config file inside the gateway directory.
	ProvidersFileName = "providers.json"
	// MaxInFlightPerProvider caps the concurrent calls of one provider.
	MaxInFlightPerProvider = 4

	retryDelayFirst  = 500 * time.Millisecond
	retryDelaySecond = 1500 * time.Millisecond
)

// State is the gateway reachability shown in the sidebar.
type State int

// Gateway states.
const (
	StateNotConfigured State = iota
	StateOnline
	StateOffline
)

// Text returns the Chinese label of the state.
func (s State) Text() string {
	switch s {
	case StateOnline:
		return "在线"
	case StateOffline:
		return "离线"
	default:
		return "未配置"
	}
}

// Snapshot is the read-only gateway status the app mirrors into the sidebar.
type Snapshot struct {
	State     State
	Model     string
	Reason    string
	UpdatedAt time.Time
}

// ProviderEntry is a provider with the masked label of its key.
type ProviderEntry struct {
	Provider  Provider
	KeyMasked string
}

// Options configure a Service.
type Options struct {
	// Dir is the gateway directory holding providers.json, the master key,
	// credentials and the usage log.
	Dir string
	// Client replaces the HTTP client of the OpenAI adapter, for tests.
	Client *http.Client
	// Now replaces the clock, for tests.
	Now func() time.Time
}

// callStatus is the outcome of the last test or real call in this process.
type callStatus struct {
	ok      bool
	message string
	at      time.Time
}

// Service is the gateway facade. It owns the configuration, the secrets and
// the usage log; it serializes writes and keeps network calls off the lock.
type Service struct {
	mu       sync.RWMutex
	cfg      *Config
	secrets  *SecretStore
	adapters *AdapterRegistry
	usage    *UsageLog
	config   string
	now      func() time.Time
	live     *callStatus
	snapshot Snapshot
	onSnap   func(Snapshot)
	sem      map[string]chan struct{}
}

// NewService loads the configuration and secrets. A malformed config or
// unreadable credentials are logged and leave a usable, empty service behind.
func NewService(opts Options) (*Service, error) {
	if opts.Dir == "" {
		return nil, errors.New("gateway: gateway directory is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	configPath := filepath.Join(opts.Dir, ProvidersFileName)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		log.Printf("gateway: %v; starting with an empty configuration", err)
	}
	secrets := NewSecretStore(opts.Dir)
	if err := secrets.Load(); err != nil {
		log.Printf("gateway: %v; providers show as unconfigured", err)
	}
	adapter := NewOpenAIAdapter()
	if opts.Client != nil {
		adapter.Client = opts.Client
	}
	adapters := NewAdapterRegistry()
	adapters.Register(adapter)
	s := &Service{
		cfg:      cfg,
		secrets:  secrets,
		adapters: adapters,
		usage:    NewUsageLog(opts.Dir),
		config:   configPath,
		now:      opts.Now,
		sem:      map[string]chan struct{}{},
	}
	s.snapshot = computeSnapshot(s.cfg, s.live, s.now())
	return s, nil
}

// SetOnSnapshot registers the status callback. It may be called from any
// goroutine; the app marshals it to the main thread.
func (s *Service) SetOnSnapshot(fn func(Snapshot)) {
	s.mu.Lock()
	s.onSnap = fn
	s.mu.Unlock()
}

// Snapshot returns the current status.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

// Entries returns the configured providers in order, with masked keys.
func (s *Service) Entries() []ProviderEntry {
	s.mu.RLock()
	cfg := s.cfg.Clone()
	s.mu.RUnlock()
	out := make([]ProviderEntry, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		out = append(out, ProviderEntry{Provider: p, KeyMasked: s.secrets.Masked(p.ID)})
	}
	return out
}

// TargetOptions lists every enabled provider model for a route select.
func (s *Service) TargetOptions() []TargetView {
	s.mu.RLock()
	cfg := s.cfg.Clone()
	s.mu.RUnlock()
	return targetOptions(cfg)
}

// RouteTargets returns the configured targets of a capability in order.
func (s *Service) RouteTargets(cap Capability) []TargetView {
	s.mu.RLock()
	cfg := s.cfg.Clone()
	s.mu.RUnlock()
	return routeViews(cfg, cap)
}

// SaveProvider creates or updates a provider. An empty ID creates one; an
// empty key keeps the stored key.
func (s *Service) SaveProvider(p Provider, key string) (Provider, error) {
	p = p.Clone()
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.TrimSpace(p.Type)
	if p.Type == "" {
		p.Type = ProviderOpenAI
	}
	if base, err := NormalizeBaseURL(p.BaseURL); err == nil {
		p.BaseURL = base
	} else {
		p.BaseURL = strings.TrimSpace(p.BaseURL)
	}
	p.Models = normalizeModels(p.Models)

	s.mu.Lock()
	cfg := s.cfg.Clone()
	hadEnabled := hasEnabledProvider(cfg)
	if p.ID == "" {
		p.ID = NewProviderID()
	}
	if i, ok := cfg.ProviderByID(p.ID); ok {
		p.LastTest = cfg.Providers[i].LastTest
		cfg.Providers[i] = p
	} else {
		p.LastTest = nil
		cfg.Providers = append(cfg.Providers, p)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		s.mu.Unlock()
		return Provider{}, err
	}
	if !hadEnabled {
		fillDefaultRoutes(cfg)
	}
	if err := SaveConfig(s.config, cfg); err != nil {
		s.mu.Unlock()
		return Provider{}, err
	}
	if key != "" {
		if err := s.secrets.Set(p.ID, key); err != nil {
			s.mu.Unlock()
			return Provider{}, err
		}
	}
	s.cfg = cfg
	saved := p
	if i, ok := cfg.ProviderByID(p.ID); ok {
		saved = cfg.Providers[i]
	}
	snap := s.snapshotLocked()
	cb := s.onSnap
	s.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
	return saved, nil
}

// SetProviderEnabled enables or disables a provider and immediately
// recomputes the snapshot.
func (s *Service) SetProviderEnabled(id string, enabled bool) error {
	s.mu.Lock()
	cfg := s.cfg.Clone()
	i, ok := cfg.ProviderByID(id)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("gateway: provider %s not found", id)
	}
	cfg.Providers[i].Enabled = enabled
	if err := SaveConfig(s.config, cfg); err != nil {
		s.mu.Unlock()
		return err
	}
	s.cfg = cfg
	snap := s.snapshotLocked()
	cb := s.onSnap
	s.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
	return nil
}

// DeleteProvider removes a provider, its key and the routes that point at it.
func (s *Service) DeleteProvider(id string) error {
	s.mu.Lock()
	cfg := s.cfg.Clone()
	if _, ok := cfg.ProviderByID(id); !ok {
		s.mu.Unlock()
		return fmt.Errorf("gateway: provider %s not found", id)
	}
	providers := cfg.Providers[:0]
	for _, p := range cfg.Providers {
		if p.ID != id {
			providers = append(providers, p)
		}
	}
	cfg.Providers = providers
	cfg.Normalize()
	if err := SaveConfig(s.config, cfg); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.secrets.Delete(id); err != nil {
		log.Printf("gateway: remove key of %s: %v", id, err)
	}
	delete(s.sem, id)
	s.cfg = cfg
	snap := s.snapshotLocked()
	cb := s.onSnap
	s.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
	return nil
}

// SaveRoutes replaces the targets of an editable capability.
func (s *Service) SaveRoutes(cap Capability, routes []Route) error {
	if !editable(cap) {
		return fmt.Errorf("gateway: capability %s is not editable yet", cap)
	}
	s.mu.Lock()
	cfg := s.cfg.Clone()
	for _, r := range routes {
		i, ok := cfg.ProviderByID(strings.TrimSpace(r.Provider))
		if !ok {
			s.mu.Unlock()
			return &ValidationError{Errors: []FieldError{{"routes", "路由目标指向不存在的供应商"}}}
		}
		if !cfg.Providers[i].HasModel(strings.TrimSpace(r.Model)) {
			s.mu.Unlock()
			return &ValidationError{Errors: []FieldError{{"routes", "路由目标指向供应商未配置的模型"}}}
		}
	}
	cfg.Routes[cap] = routes
	cfg.Normalize()
	if err := SaveConfig(s.config, cfg); err != nil {
		s.mu.Unlock()
		return err
	}
	s.cfg = cfg
	snap := s.snapshotLocked()
	cb := s.onSnap
	s.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
	return nil
}

func editable(cap Capability) bool {
	for _, c := range EditableCapabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// TestProvider calls /models of a saved provider, persists the result and
// returns it. It does not spend tokens and does not write usage.
func (s *Service) TestProvider(ctx context.Context, id string) (TestResult, error) {
	s.mu.RLock()
	i, ok := s.cfg.ProviderByID(id)
	var p Provider
	if ok {
		p = s.cfg.Providers[i].Clone()
	}
	s.mu.RUnlock()
	if !ok {
		return TestResult{}, fmt.Errorf("gateway: provider %s not found", id)
	}
	if !p.Enabled {
		return TestResult{}, fmt.Errorf("gateway: provider is disabled; enable it before testing")
	}
	adapter, ok := s.adapters.Get(p.Type)
	if !ok {
		return TestResult{}, fmt.Errorf("gateway: unsupported provider type %q", p.Type)
	}
	key := s.secrets.Secret(p.ID)
	result, models, err := adapter.Test(ctx, p, key)
	if err != nil {
		result = TestResult{
			OK:      false,
			Kind:    TestKindModels,
			Message: KindText(KindOf(err)) + " · " + err.Error(),
			At:      s.now().Unix(),
		}
	}

	s.mu.Lock()
	next := s.cfg.Clone()
	j, exists := next.ProviderByID(id)
	if exists {
		next.Providers[j].LastTest = &result
		if result.OK && len(next.Providers[j].Models) == 0 {
			next.Providers[j].Models = modelsFromIDs(models)
			fillDefaultRoutes(next)
		}
		next.Normalize()
		if saveErr := SaveConfig(s.config, next); saveErr != nil {
			s.mu.Unlock()
			return result, saveErr
		}
		s.cfg = next
	}
	s.live = &callStatus{ok: result.OK, message: result.Message, at: time.Unix(result.At, 0)}
	snap := s.snapshotLocked()
	cb := s.onSnap
	s.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
	return result, err
}

// Chat calls the primary chat target and falls back in route order.
func (s *Service) Chat(ctx context.Context, req ChatRequest, origin string) (ChatResponse, error) {
	targets := s.targets(CapabilityChat)
	if len(targets) == 0 {
		return ChatResponse{}, internalError("模型未配置，请先在设置中配置对话模型", nil)
	}
	var lastErr error
	for _, t := range targets {
		start := time.Now()
		resp, err := s.callChat(ctx, t, req)
		model := resp.Model
		if model == "" {
			model = t.Model
		}
		s.recordUsage(origin, t, model, resp.InputTokens, resp.OutputTokens, resp.HasUsage, time.Since(start), err)
		if err == nil {
			s.setLiveOutcome(nil)
			return resp, nil
		}
		lastErr = err
		if !FallbackAllowed(err) {
			s.setLiveOutcome(err)
			return ChatResponse{}, err
		}
	}
	s.setLiveOutcome(lastErr)
	return ChatResponse{}, lastErr
}

// ChatProvider calls one chosen target, for the settings page test.
func (s *Service) ChatProvider(ctx context.Context, providerID, model string, req ChatRequest, origin string) (ChatResponse, error) {
	s.mu.RLock()
	i, ok := s.cfg.ProviderByID(providerID)
	var p Provider
	if ok {
		p = s.cfg.Providers[i].Clone()
	}
	s.mu.RUnlock()
	if !ok {
		return ChatResponse{}, internalError("供应商不存在", nil)
	}
	if !p.Enabled {
		return ChatResponse{}, internalError("供应商已停用", nil)
	}
	if !p.HasModel(model) {
		return ChatResponse{}, internalError("模型不在供应商的模型列表中", nil)
	}
	t := Target{Provider: p, Model: model, Capability: CapabilityChat}
	start := time.Now()
	resp, err := s.callChat(ctx, t, req)
	actual := resp.Model
	if actual == "" {
		actual = model
	}
	s.recordUsage(origin, t, actual, resp.InputTokens, resp.OutputTokens, resp.HasUsage, time.Since(start), err)
	s.setLiveOutcome(err)
	if err != nil {
		return ChatResponse{}, err
	}
	return resp, nil
}

// Embed calls the primary embedding target and falls back in route order.
func (s *Service) Embed(ctx context.Context, req EmbedRequest, origin string) (EmbedResponse, error) {
	targets := s.targets(CapabilityEmbedding)
	if len(targets) == 0 {
		return EmbedResponse{}, internalError("向量模型未配置", nil)
	}
	var lastErr error
	for _, t := range targets {
		start := time.Now()
		resp, err := s.callEmbed(ctx, t, req)
		model := resp.Model
		if model == "" {
			model = t.Model
		}
		s.recordUsage(origin, t, model, resp.InputTokens, 0, resp.HasUsage, time.Since(start), err)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !FallbackAllowed(err) {
			return EmbedResponse{}, err
		}
	}
	return EmbedResponse{}, lastErr
}

func (s *Service) targets(cap Capability) []Target {
	s.mu.RLock()
	targets := resolveTargets(s.cfg, s.adapters, cap)
	s.mu.RUnlock()
	return targets
}

func (s *Service) callChat(ctx context.Context, t Target, req ChatRequest) (ChatResponse, error) {
	adapter, ok := s.adapters.Get(t.Provider.Type)
	if !ok {
		return ChatResponse{}, internalError("供应商类型不受支持", nil)
	}
	key := s.secrets.Secret(t.Provider.ID)
	req.Model = t.Model
	return retryValue(ctx, func() (ChatResponse, error) {
		release, err := s.acquire(ctx, t.Provider.ID)
		if err != nil {
			return ChatResponse{}, err
		}
		defer release()
		return adapter.Chat(ctx, t.Provider, key, req)
	})
}

func (s *Service) callEmbed(ctx context.Context, t Target, req EmbedRequest) (EmbedResponse, error) {
	adapter, ok := s.adapters.Get(t.Provider.Type)
	if !ok {
		return EmbedResponse{}, internalError("供应商类型不受支持", nil)
	}
	key := s.secrets.Secret(t.Provider.ID)
	req.Model = t.Model
	return retryValue(ctx, func() (EmbedResponse, error) {
		release, err := s.acquire(ctx, t.Provider.ID)
		if err != nil {
			return EmbedResponse{}, err
		}
		defer release()
		return adapter.Embed(ctx, t.Provider, key, req)
	})
}

// retryValue repeats fn at most twice, only for network, timeout and server
// failures, with the delays of 3.4; the waits respect the context.
func retryValue[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	var err error
	for attempt := 0; attempt <= 2; attempt++ {
		if attempt > 0 {
			delay := retryDelayFirst
			if attempt == 2 {
				delay = retryDelaySecond
			}
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return zero, classifyTransport(ctx, ctx.Err())
			}
		}
		var out T
		out, err = fn()
		if err == nil {
			return out, nil
		}
		if !Retryable(err) {
			return zero, err
		}
	}
	return zero, err
}

func (s *Service) acquire(ctx context.Context, providerID string) (func(), error) {
	s.mu.Lock()
	sem := s.sem[providerID]
	if sem == nil {
		sem = make(chan struct{}, MaxInFlightPerProvider)
		s.sem[providerID] = sem
	}
	s.mu.Unlock()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, internalError("排队等待超时", ctx.Err())
	}
}

func (s *Service) recordUsage(origin string, t Target, model string, inputTokens, outputTokens int, hasUsage bool, duration time.Duration, err error) {
	rec := UsageRecord{
		Time:           s.now().Unix(),
		Origin:         origin,
		ProviderID:     t.Provider.ID,
		ProviderName:   t.Provider.Name,
		Model:          model,
		Capability:     t.Capability,
		InputTokens:    inputTokens,
		OutputTokens:   outputTokens,
		UsageEstimated: !hasUsage,
		DurationMS:     duration.Milliseconds(),
		Status:         usageStatus(err),
		ErrorCode:      usageErrorCode(err),
	}
	if writeErr := s.usage.Append(rec); writeErr != nil {
		log.Printf("gateway: write usage: %v", writeErr)
	}
}

func (s *Service) setLiveOutcome(err error) {
	status := &callStatus{ok: err == nil, at: s.now()}
	if err != nil {
		status.message = err.Error()
	}
	s.mu.Lock()
	s.live = status
	snap := s.snapshotLocked()
	cb := s.onSnap
	s.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
}

func (s *Service) snapshotLocked() Snapshot {
	s.snapshot = computeSnapshot(s.cfg, s.live, s.now())
	return s.snapshot
}

// computeSnapshot derives the sidebar status from the config and the last
// in-process test or call.
func computeSnapshot(cfg *Config, live *callStatus, now time.Time) Snapshot {
	if _, ok := firstEnabledProvider(cfg); !ok {
		return Snapshot{State: StateNotConfigured, UpdatedAt: now}
	}
	model := ""
	var candidate *Provider
	for _, r := range cfg.Routes[CapabilityChat] {
		i, ok := cfg.ProviderByID(r.Provider)
		if !ok || !cfg.Providers[i].Enabled {
			continue
		}
		p := cfg.Providers[i]
		candidate = &p
		model = r.Model
		break
	}
	if candidate == nil {
		if p, ok := firstEnabledProvider(cfg); ok {
			candidate = &p
		}
	}
	if candidate == nil {
		return Snapshot{State: StateNotConfigured, UpdatedAt: now}
	}

	ok := false
	reason := "尚未连接测试"
	at := time.Time{}
	if candidate.LastTest != nil {
		ok = candidate.LastTest.OK
		at = time.Unix(candidate.LastTest.At, 0)
		if ok {
			reason = ""
		} else {
			reason = candidate.LastTest.Message
		}
	}
	if live != nil && live.at.After(at) {
		ok = live.ok
		at = live.at
		if ok {
			reason = ""
		} else {
			reason = live.message
		}
	}
	state := StateOffline
	if ok {
		state = StateOnline
	}
	return Snapshot{State: state, Model: model, Reason: reason, UpdatedAt: now}
}
