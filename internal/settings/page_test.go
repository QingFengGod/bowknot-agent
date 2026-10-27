package settings

import (
	"context"
	"errors"
	"testing"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

type fakeGateway struct {
	snapshot   gateway.Snapshot
	entries    []gateway.ProviderEntry
	options    []gateway.TargetView
	routes     map[gateway.Capability][]gateway.TargetView
	saveErr    error
	saved      []gateway.Provider
	savedKey   string
	deleted    []string
	enabled    map[string]bool
	routeSaved map[gateway.Capability][]gateway.Route
	testResult gateway.TestResult
	testErr    error
	chatResp   gateway.ChatResponse
	chatErr    error
	lastChat   gateway.ChatRequest
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{
		routes:     map[gateway.Capability][]gateway.TargetView{},
		enabled:    map[string]bool{},
		routeSaved: map[gateway.Capability][]gateway.Route{},
	}
}

func (f *fakeGateway) Snapshot() gateway.Snapshot { return f.snapshot }

func (f *fakeGateway) Entries() []gateway.ProviderEntry { return f.entries }

func (f *fakeGateway) TargetOptions() []gateway.TargetView { return f.options }

func (f *fakeGateway) RouteTargets(cap gateway.Capability) []gateway.TargetView {
	return f.routes[cap]
}

func (f *fakeGateway) SaveProvider(p gateway.Provider, key string) (gateway.Provider, error) {
	if f.saveErr != nil {
		return gateway.Provider{}, f.saveErr
	}
	if p.ID == "" {
		p.ID = "p-00000001"
	}
	f.saved = append(f.saved, p)
	f.savedKey = key
	f.entries = append(f.entries, gateway.ProviderEntry{
		Provider: p, KeyMasked: gateway.Mask(key)})
	return p, nil
}

func (f *fakeGateway) SetProviderEnabled(id string, enabled bool) error {
	f.enabled[id] = enabled
	return nil
}

func (f *fakeGateway) DeleteProvider(id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeGateway) SaveRoutes(cap gateway.Capability, routes []gateway.Route) error {
	if !editable(cap) {
		return errors.New("read-only capability")
	}
	f.routeSaved[cap] = routes
	f.routes[cap] = nil
	for _, r := range routes {
		name := r.Provider
		for _, entry := range f.entries {
			if entry.Provider.ID == r.Provider {
				name = entry.Provider.Name
			}
		}
		f.routes[cap] = append(f.routes[cap],
			gateway.TargetView{ProviderID: r.Provider, ProviderName: name, Model: r.Model})
	}
	return nil
}

func (f *fakeGateway) TestProvider(ctx context.Context, id string) (gateway.TestResult, error) {
	return f.testResult, f.testErr
}

func (f *fakeGateway) ChatProvider(ctx context.Context, providerID, model string, req gateway.ChatRequest, origin string) (gateway.ChatResponse, error) {
	f.lastChat = req
	return f.chatResp, f.chatErr
}

func editable(cap gateway.Capability) bool {
	for _, c := range gateway.EditableCapabilities {
		if c == cap {
			return true
		}
	}
	return false
}

func newTester(t *testing.T, p *Page) *mygoui.Tester {
	t.Helper()
	p.Dispatch = func(fn func()) { fn() }
	return mygoui.NewTester(p.View(), 1100, 1600)
}

func sampleEntry(id, name, masked string, models ...string) gateway.ProviderEntry {
	last := &gateway.TestResult{OK: true, Kind: gateway.TestKindModels,
		Message: "2 个模型可用", At: 1790000000}
	return gateway.ProviderEntry{
		Provider: gateway.Provider{ID: id, Name: name, Type: gateway.ProviderOpenAI,
			BaseURL: "https://api.example.com/v1", Enabled: true,
			Models: models, LastTest: last},
		KeyMasked: masked,
	}
}

func TestSettingsEmptyState(t *testing.T) {
	p := NewPage(newFakeGateway())
	tester := newTester(t, p)
	for _, text := range []string{
		"设置", "模型服务与网关", "网关未配置", "模型未配置",
		"还没有配置模型服务", "添加供应商", "能力路由", "对话测试", "其他能力",
		"随对应模块接入后开放",
	} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
}

func TestSettingsAddProviderForm(t *testing.T) {
	fake := newFakeGateway()
	p := NewPage(fake)
	tester := newTester(t, p)
	if err := tester.Click("添加供应商"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !p.FormOpen || !tester.HasText("编辑供应商") && !tester.HasText("添加供应商") {
		t.Fatal("editor did not open")
	}
	p.FormName = "本地模型"
	p.FormBaseURL = "https://api.example.com/v1"
	p.FormKey = "sk-test-1234"
	p.FormModels = "gpt-4o-mini\n\ngpt-4o\n"
	tester.Frame()
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.FormOpen {
		t.Error("editor stayed open after a successful save")
	}
	if len(fake.saved) != 1 {
		t.Fatalf("saved = %d providers, want 1", len(fake.saved))
	}
	saved := fake.saved[0]
	if saved.Name != "本地模型" || saved.BaseURL != "https://api.example.com/v1" ||
		saved.Type != gateway.ProviderOpenAI || !saved.Enabled {
		t.Errorf("saved = %+v", saved)
	}
	if len(saved.Models) != 2 {
		t.Errorf("models = %+v, want 2", saved.Models)
	}
	if fake.savedKey != "sk-test-1234" {
		t.Errorf("key = %q", fake.savedKey)
	}
	if !tester.HasText("本地模型") {
		t.Error("provider list does not show the new provider")
	}
	if p.FormKey != "" {
		t.Error("plaintext key kept after saving")
	}
}

func TestSettingsSaveShowsValidationErrors(t *testing.T) {
	fake := newFakeGateway()
	fake.saveErr = &gateway.ValidationError{Errors: []gateway.FieldError{
		{Field: "base_url", Message: "供应商地址须包含 API 版本路径，例如 /v1"},
	}}
	p := NewPage(fake)
	tester := newTester(t, p)
	p.FormOpen = true
	p.FormName = "A"
	p.FormBaseURL = "https://api.example.com"
	tester.Frame()
	if err := tester.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.FormError == "" || !p.FormOpen {
		t.Fatalf("FormError = %q, open = %v", p.FormError, p.FormOpen)
	}
	if !tester.HasText("base_url") {
		t.Error("validation error is not shown in the editor")
	}
}

func TestSettingsProviderRowAndMask(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{
		sampleEntry("p-00000001", "OpenAI", "••••1234", "gpt-4o-mini"),
	}
	fake.snapshot = gateway.Snapshot{State: gateway.StateOnline, Model: "gpt-4o-mini"}
	p := NewPage(fake)
	tester := newTester(t, p)
	for _, text := range []string{
		"网关在线", "当前模型：gpt-4o-mini", "OpenAI", "OpenAI 兼容",
		"密钥 ••••1234", "成功 · 2 个模型可用", "编辑", "测试", "删除",
	} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
	if tester.HasText("sk-") {
		t.Error("view renders a plaintext key")
	}
}

func TestSettingsEditKeepsOldKey(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{
		sampleEntry("p-00000001", "OpenAI", "••••1234", "m1"),
	}
	p := NewPage(fake)
	tester := newTester(t, p)
	if err := tester.Click("编辑 OpenAI"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !p.FormOpen || p.FormID != "p-00000001" || p.FormKey != "" {
		t.Fatalf("editor = %+v", p)
	}
	if p.FormModels != "m1" {
		t.Errorf("models = %q", p.FormModels)
	}
	tester.Frame()
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.FormOpen {
		t.Error("editor stayed open after cancel")
	}
}

func TestSettingsDeleteConfirms(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{sampleEntry("p-00000001", "OpenAI", "已配置", "m1")}
	p := NewPage(fake)
	tester := newTester(t, p)
	if err := tester.Click("删除 OpenAI"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("确定删除") {
		t.Fatal("confirmation is not shown")
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(fake.deleted) != 1 || fake.deleted[0] != "p-00000001" {
		t.Errorf("deleted = %v", fake.deleted)
	}
}

func TestSettingsToggleProvider(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{sampleEntry("p-00000001", "OpenAI", "已配置", "m1")}
	p := NewPage(fake)
	tester := newTester(t, p)
	if err := tester.Click("启用 OpenAI"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if enabled, ok := fake.enabled["p-00000001"]; !ok || enabled {
		t.Errorf("SetProviderEnabled = %v, %v; want false", enabled, ok)
	}
}

func TestSettingsRoutePrimaryAndSave(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{
		sampleEntry("p-00000001", "OpenAI", "已配置", "m1", "m2"),
	}
	fake.options = []gateway.TargetView{
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m1"},
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m2"},
	}
	fake.routes[gateway.CapabilityChat] = []gateway.TargetView{
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m1"},
	}
	p := NewPage(fake)
	tester := newTester(t, p)

	st := p.Routes[gateway.CapabilityChat]
	if st.primary != "OpenAI · m1" {
		t.Fatalf("primary = %q, want OpenAI · m1", st.primary)
	}
	st.primary = "OpenAI · m2"
	p.applyPrimary(gateway.CapabilityChat)
	if len(st.draft) != 1 || st.draft[0].Model != "m2" {
		t.Errorf("draft = %+v, want m2", st.draft)
	}
	tester.Frame()
	if err := tester.Click("保存路由"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	saved := fake.routeSaved[gateway.CapabilityChat]
	if len(saved) != 1 || saved[0].Model != "m2" {
		t.Errorf("saved routes = %+v", saved)
	}
	if !tester.HasText("路由已保存") {
		t.Error("save notice is not shown")
	}
}

func TestSettingsBackupRouteAddAndRemove(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{
		sampleEntry("p-00000001", "OpenAI", "已配置", "m1", "m2"),
	}
	fake.options = []gateway.TargetView{
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m1"},
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m2"},
	}
	p := NewPage(fake)
	tester := newTester(t, p)
	st := p.Routes[gateway.CapabilityChat]
	st.draft = []gateway.Route{{Provider: "p-00000001", Model: "m1"}}
	st.add = "OpenAI · m2"
	tester.Frame()
	if err := tester.Click("添加备用"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(st.draft) != 2 || st.draft[1].Model != "m2" {
		t.Fatalf("draft = %+v, want m2 as backup", st.draft)
	}
	if err := tester.Click("移除"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(st.draft) != 1 {
		t.Errorf("draft = %+v, want only the primary", st.draft)
	}
}

func TestSettingsChatTestShowsReply(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{sampleEntry("p-00000001", "OpenAI", "已配置", "m1")}
	fake.options = []gateway.TargetView{
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m1"},
	}
	fake.routes[gateway.CapabilityChat] = []gateway.TargetView{
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m1"},
	}
	fake.chatResp = gateway.ChatResponse{Content: "你好，我是模型", Model: "m1",
		InputTokens: 3, OutputTokens: 5, HasUsage: true}
	p := NewPage(fake)
	tester := newTester(t, p)
	p.Prompt = "你好"
	tester.Frame()
	if err := tester.Click("发送"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.ChatReply == nil || !tester.HasText("你好，我是模型") {
		t.Fatal("reply is not shown")
	}
	for _, text := range []string{"m1", "输入 3 · 输出 5 token"} {
		if !tester.HasText(text) {
			t.Errorf("view does not show %q", text)
		}
	}
	if p.Prompt != "你好" {
		t.Errorf("prompt = %q", p.Prompt)
	}
	if fake.lastChat.MaxTokens != ChatTestMaxTokens {
		t.Errorf("max tokens = %d, want %d", fake.lastChat.MaxTokens, ChatTestMaxTokens)
	}
	if len(fake.lastChat.Messages) != 1 || fake.lastChat.Messages[0].Content != "你好" {
		t.Errorf("messages = %+v", fake.lastChat.Messages)
	}
}

func TestSettingsChatTestError(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{sampleEntry("p-00000001", "OpenAI", "已配置", "m1")}
	fake.options = []gateway.TargetView{
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m1"},
	}
	fake.routes[gateway.CapabilityChat] = []gateway.TargetView{
		{ProviderID: "p-00000001", ProviderName: "OpenAI", Model: "m1"},
	}
	fake.chatErr = &gateway.Error{Kind: gateway.KindAuth, Message: "鉴权失败（HTTP 401）"}
	p := NewPage(fake)
	tester := newTester(t, p)
	p.Prompt = "你好"
	tester.Frame()
	if err := tester.Click("发送"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.ChatReply != nil || p.ChatError == "" {
		t.Fatalf("reply = %+v, error = %q", p.ChatReply, p.ChatError)
	}
	if !tester.HasText("鉴权") {
		t.Error("error classification is not shown")
	}
}

func TestSettingsChatDisabledWithoutTarget(t *testing.T) {
	p := NewPage(newFakeGateway())
	tester := newTester(t, p)
	if !tester.HasText("模型未配置") {
		t.Error("disabled reason is missing")
	}
	if err := tester.Click("发送"); err != nil {
		t.Fatalf("disabled send button is not clickable: %v", err)
	}
	if p.ChatSending || p.ChatReply != nil || p.ChatError != "" {
		t.Errorf("disabled send ran a call: %+v", p)
	}
}

func TestSettingsTestProviderResult(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{sampleEntry("p-00000001", "OpenAI", "已配置", "m1")}
	fake.testResult = gateway.TestResult{OK: true, Kind: gateway.TestKindModels,
		Message: "3 个模型可用", At: 1790000000}
	p := NewPage(fake)
	tester := newTester(t, p)
	if err := tester.Click("测试 OpenAI"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if p.TestingID != "" {
		t.Errorf("TestingID = %q, want empty", p.TestingID)
	}
	if !tester.HasText("测试成功 · 3 个模型可用") {
		t.Error("test result is not shown")
	}
}

func TestSettingsTestProviderFailure(t *testing.T) {
	fake := newFakeGateway()
	fake.entries = []gateway.ProviderEntry{sampleEntry("p-00000001", "OpenAI", "已配置", "m1")}
	fake.testErr = &gateway.Error{Kind: gateway.KindAuth, Message: "鉴权失败（HTTP 401）"}
	p := NewPage(fake)
	tester := newTester(t, p)
	if err := tester.Click("测试 OpenAI"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("鉴权") {
		t.Error("failure classification is not shown")
	}
}
