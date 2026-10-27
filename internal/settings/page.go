package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

// OriginSettings marks usage written by the settings page test call.
const OriginSettings = "settings"

// ProviderTypeLabel is the only provider type the page offers.
const ProviderTypeLabel = "OpenAI 兼容"

const (
	noneTarget       = "未配置"
	addTargetDefault = "选择要添加的目标"
)

// ChatTestMaxTokens keeps the settings test cheap.
const ChatTestMaxTokens = 256

// Gateway is the part of the gateway the settings page uses. The app injects
// a *gateway.Service; tests inject a stand-in.
type Gateway interface {
	Snapshot() gateway.Snapshot
	Entries() []gateway.ProviderEntry
	TargetOptions() []gateway.TargetView
	RouteTargets(cap gateway.Capability) []gateway.TargetView
	SaveProvider(p gateway.Provider, key string) (gateway.Provider, error)
	SetProviderEnabled(id string, enabled bool) error
	DeleteProvider(id string) error
	SaveRoutes(cap gateway.Capability, routes []gateway.Route) error
	TestProvider(ctx context.Context, id string) (gateway.TestResult, error)
	ChatProvider(ctx context.Context, providerID, model string, req gateway.ChatRequest, origin string) (gateway.ChatResponse, error)
}

// routeState is the unsaved route draft of one editable capability.
type routeState struct {
	draft   []gateway.Route
	primary string
	add     string
	notice  string
}

// Page is the settings module: model providers, routes and a single-turn
// chat test. The app holds one Page for the life of the process, so switching
// modules keeps the editor and test results.
type Page struct {
	Gateway Gateway
	// Repaint repaints the main window after an async result.
	Repaint func()
	// Dispatch runs a UI update on the main thread; mygo.RunOnMain by default.
	Dispatch func(func())

	// Provider editor state; FormOpen tells whether the inline panel shows.
	FormOpen    bool
	FormID      string
	FormName    string
	FormBaseURL string
	FormKey     string
	FormModels  string
	FormEnabled bool
	FormError   string

	// ProviderNotice is the last provider action error or test notice.
	ProviderNotice string
	// TestingID is the provider whose connection test runs.
	TestingID string
	// DeletingID is the provider whose deletion awaits confirmation.
	DeletingID string

	// Routes are the drafts of chat and embedding.
	Routes map[gateway.Capability]*routeState

	// Chat test state.
	Prompt      string
	ChatTarget  string
	ChatSending bool
	ChatReply   *gateway.ChatResponse
	ChatError   string

	enabled map[string]*bool
}

// NewPage creates the settings page and loads the current drafts.
func NewPage(gw Gateway) *Page {
	p := &Page{
		Gateway:     gw,
		FormEnabled: true,
		Routes: map[gateway.Capability]*routeState{
			gateway.CapabilityChat:      {},
			gateway.CapabilityEmbedding: {},
		},
		enabled: map[string]*bool{},
	}
	p.reloadRoutes()
	p.resetChatTarget()
	return p
}

// View returns the view of the settings module.
func (p *Page) View() func(c *mygoui.Context) {
	return func(c *mygoui.Context) {
		t := c.Theme()
		area := mygoui.Column(c).Fill().Padding(32).Gap(16).Background(t.Background)
		area.Children(func() {
			p.header(c, t)
			scroll := mygoui.Scroll(c).Grow(1).FillHeight()
			scroll.Children(func() {
				body := mygoui.Column(c).FillWidth().Gap(16)
				body.Children(func() {
					p.providers(c, t)
					p.routesView(c, t)
					p.chatTest(c, t)
					p.readOnly(c, t)
				})
			})
		})
	}
}

func (p *Page) snapshot() gateway.Snapshot {
	if p.Gateway == nil {
		return gateway.Snapshot{}
	}
	return p.Gateway.Snapshot()
}

func (p *Page) header(c *mygoui.Context, t *mygoui.Theme) {
	snap := p.snapshot()
	mode := ui.ModeLight
	if t.Dark {
		mode = ui.ModeDark
	}
	color := t.TextMuted
	switch snap.State {
	case gateway.StateOnline:
		color = ui.SuccessColor(mode)
	case gateway.StateOffline:
		color = ui.DangerColor(mode)
	}
	head := mygoui.Column(c).Gap(8).Shrink(0)
	head.Children(func() {
		text := mygoui.Column(c).Gap(4)
		text.Children(func() {
			mygoui.Text(c, "设置").FontSize(24).Bold().TextColor(t.Text)
			mygoui.Text(c, "模型服务与网关").FontSize(13).TextColor(t.TextMuted)
		})
		status := mygoui.Row(c).Gap(12).AlignItems(mygoui.Center)
		status.Children(func() {
			dot := mygoui.Row(c).Gap(6).AlignItems(mygoui.Center)
			dot.Children(func() {
				mygoui.Box(c).Size(8, 8).Radius(4).Background(color)
				mygoui.Text(c, "网关"+snap.State.Text()).FontSize(12).TextColor(color)
			})
			model := snap.Model
			if model == "" {
				model = "模型未配置"
			}
			mygoui.Text(c, "当前模型："+model).FontSize(12).TextColor(t.TextMuted).SingleLine()
			if snap.Reason != "" {
				mygoui.Text(c, "最近："+snap.Reason).FontSize(12).
					TextColor(t.TextMuted).SingleLine()
			}
		})
	})
}

func (p *Page) providers(c *mygoui.Context, t *mygoui.Theme) {
	entries := p.entries()
	card := mygoui.Column(c).FillWidth().Padding(16).Gap(10).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		title := mygoui.Row(c).FillWidth().Gap(12).AlignItems(mygoui.Center)
		title.Children(func() {
			mygoui.Text(c, "模型服务").FontSize(15).Bold().TextColor(t.Text)
		})
		if len(entries) == 0 && !p.FormOpen {
			empty := mygoui.Column(c).FillWidth().Padding(12).Gap(8).
				AlignItems(mygoui.Center)
			empty.Children(func() {
				mygoui.Text(c, "还没有配置模型服务").FontSize(14).TextColor(t.Text)
				mygoui.Text(c, "添加一个 OpenAI 兼容服务后即可测试对话").
					FontSize(12).TextColor(t.TextMuted)
				p.button(c, t, "添加供应商", true, false, p.openNewForm)
			})
		}
		for _, entry := range entries {
			p.providerRow(c, t, entry)
		}
		if len(entries) > 0 && !p.FormOpen {
			row := mygoui.Row(c).FillWidth().AlignItems(mygoui.Center)
			row.Children(func() {
				p.button(c, t, "添加供应商", true, false, p.openNewForm)
			})
		}
		if p.ProviderNotice != "" {
			mygoui.Text(c, p.ProviderNotice).FontSize(11).
				TextColor(ui.DangerColor(p.mode(t))).Wrap()
		}
		if p.FormOpen {
			p.editor(c, t)
		}
	})
}

func (p *Page) entries() []gateway.ProviderEntry {
	if p.Gateway == nil {
		return nil
	}
	return p.Gateway.Entries()
}

func (p *Page) providerRow(c *mygoui.Context, t *mygoui.Theme, entry gateway.ProviderEntry) {
	prov := entry.Provider
	row := mygoui.Column(c).FillWidth().Padding(10).Gap(6).Radius(t.Radius).
		Background(t.Background).BorderWidth(1).BorderColor(t.Border)
	row.Children(func() {
		head := mygoui.Row(c).FillWidth().Gap(12).AlignItems(mygoui.Center)
		head.Children(func() {
			info := mygoui.Column(c).Grow(1).Gap(2)
			info.Children(func() {
				mygoui.Text(c, prov.Name).FontSize(14).Bold().TextColor(t.Text).SingleLine()
				mygoui.Text(c, ProviderTypeLabel+" · "+prov.BaseURL).FontSize(11).
					TextColor(t.TextMuted).SingleLine()
				mygoui.Text(c, "密钥 "+entry.KeyMasked).FontSize(11).
					TextColor(t.TextMuted).SingleLine()
				mygoui.Text(c, lastTestText(prov)).FontSize(11).
					TextColor(t.TextMuted).SingleLine()
			})
			enabled := p.enabledPtr(prov.ID, prov.Enabled)
			check := mygoui.Checkbox(c, enabled, "启用").Label("启用 " + prov.Name)
			if check.Changed() {
				p.setEnabled(prov.ID, *enabled)
			}
			p.buttonLabeled(c, t, "编辑", "编辑 "+prov.Name, false, false, func() { p.openEditForm(prov) })
			testing := p.TestingID == prov.ID
			label := "测试"
			if testing {
				label = "测试中…"
			}
			p.buttonLabeled(c, t, label, "测试 "+prov.Name, false, testing, func() {
				if !testing {
					p.startTest(prov.ID)
				}
			})
			p.buttonLabeled(c, t, "删除", "删除 "+prov.Name, false, p.TestingID == prov.ID, func() {
				p.DeletingID = prov.ID
			})
		})
		if p.DeletingID == prov.ID {
			confirm := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
			confirm.Children(func() {
				mygoui.Text(c, "删除后配置与密钥一并清除，确定删除？").
					FontSize(12).TextColor(ui.DangerColor(p.mode(t))).Grow(1).Wrap()
				p.button(c, t, "取消删除", false, false, func() { p.DeletingID = "" })
				p.button(c, t, "确认删除", true, false, func() { p.deleteProvider(prov.ID) })
			})
		}
	})
}

func lastTestText(prov gateway.Provider) string {
	if prov.LastTest == nil {
		return "尚未连接测试"
	}
	status := "成功"
	if !prov.LastTest.OK {
		status = "失败"
	}
	at := time.Unix(prov.LastTest.At, 0).Format("01-02 15:04")
	return fmt.Sprintf("%s · %s · %s", status, prov.LastTest.Message, at)
}

func (p *Page) enabledPtr(id string, current bool) *bool {
	if p.enabled == nil {
		p.enabled = map[string]*bool{}
	}
	ptr, ok := p.enabled[id]
	if !ok {
		ptr = new(bool)
		*ptr = current
		p.enabled[id] = ptr
	}
	return ptr
}

func (p *Page) editor(c *mygoui.Context, t *mygoui.Theme) {
	panel := mygoui.Column(c).FillWidth().Padding(12).Gap(10).Radius(t.Radius).
		Background(t.Background).BorderWidth(1).BorderColor(t.Accent)
	panel.Children(func() {
		title := "添加供应商"
		if p.FormID != "" {
			title = "编辑供应商"
		}
		mygoui.Text(c, title).FontSize(14).Bold().TextColor(t.Text)
		p.field(c, t, "名称", func() {
			mygoui.TextInput(c, &p.FormName).Width(360).
				Label("名称").Placeholder("例如 OpenAI")
		})
		p.field(c, t, "类型", func() {
			mygoui.Text(c, ProviderTypeLabel).FontSize(13).TextColor(t.TextMuted)
		})
		p.field(c, t, "地址", func() {
			mygoui.TextInput(c, &p.FormBaseURL).Width(420).
				Label("地址").Placeholder("例如 https://api.openai.com/v1")
		})
		p.field(c, t, "密钥", func() {
			mygoui.TextInput(c, &p.FormKey).Width(420).Password().
				Label("密钥").Placeholder("留空保留已保存的密钥")
		})
		p.field(c, t, "模型（每行一个）", func() {
			mygoui.TextArea(c, &p.FormModels).Width(420).Height(90).
				Label("模型")
		})
		mygoui.Checkbox(c, &p.FormEnabled, "启用该供应商").Label("启用该供应商")
		if p.FormError != "" {
			mygoui.Text(c, p.FormError).FontSize(12).
				TextColor(ui.DangerColor(p.mode(t))).Wrap()
		}
		actions := mygoui.Row(c).Gap(8).AlignItems(mygoui.Center)
		actions.Children(func() {
			p.button(c, t, "保存", true, false, p.saveForm)
			p.button(c, t, "取消", false, false, p.closeForm)
			if p.FormID != "" {
				disabled := p.TestingID == p.FormID
				label := "测试"
				if disabled {
					label = "测试中…"
				}
				p.button(c, t, label, false, disabled, func() {
					p.startTest(p.FormID)
				})
				mygoui.Text(c, "测试使用已保存的配置").FontSize(11).TextColor(t.TextMuted)
			}
		})
	})
}

func (p *Page) field(c *mygoui.Context, t *mygoui.Theme, label string, build func()) {
	column := mygoui.Column(c).Gap(4)
	column.Children(func() {
		mygoui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		build()
	})
}

func (p *Page) openNewForm() {
	p.FormOpen = true
	p.FormID = ""
	p.FormName = ""
	p.FormBaseURL = ""
	p.FormKey = ""
	p.FormModels = ""
	p.FormEnabled = true
	p.FormError = ""
}

func (p *Page) openEditForm(prov gateway.Provider) {
	p.FormOpen = true
	p.FormID = prov.ID
	p.FormName = prov.Name
	p.FormBaseURL = prov.BaseURL
	p.FormKey = ""
	p.FormModels = strings.Join(prov.Models, "\n")
	p.FormEnabled = prov.Enabled
	p.FormError = ""
}

func (p *Page) closeForm() {
	p.FormOpen = false
	p.FormID = ""
	p.FormKey = ""
	p.FormError = ""
}

func (p *Page) saveForm() {
	if p.Gateway == nil {
		return
	}
	prov := gateway.Provider{
		ID:      p.FormID,
		Name:    p.FormName,
		Type:    gateway.ProviderOpenAI,
		BaseURL: p.FormBaseURL,
		Enabled: p.FormEnabled,
		Models:  splitModels(p.FormModels),
	}
	saved, err := p.Gateway.SaveProvider(prov, p.FormKey)
	if err != nil {
		p.FormError = validationText(err)
		return
	}
	delete(p.enabled, saved.ID)
	p.closeForm()
	p.ProviderNotice = ""
	p.reloadRoutes()
	p.resetChatTarget()
	p.repaint()
}

func splitModels(text string) []string {
	var models []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			models = append(models, line)
		}
	}
	return models
}

func validationText(err error) string {
	var verr *gateway.ValidationError
	if errors.As(err, &verr) && len(verr.Errors) > 0 {
		parts := make([]string, 0, len(verr.Errors))
		for _, fe := range verr.Errors {
			parts = append(parts, fe.Field+": "+fe.Message)
		}
		return strings.Join(parts, "；")
	}
	return err.Error()
}

func (p *Page) setEnabled(id string, enabled bool) {
	if p.Gateway == nil {
		return
	}
	if err := p.Gateway.SetProviderEnabled(id, enabled); err != nil {
		p.ProviderNotice = err.Error()
		return
	}
	p.ProviderNotice = ""
	p.reloadRoutes()
	p.resetChatTarget()
	p.repaint()
}

func (p *Page) deleteProvider(id string) {
	if p.Gateway == nil {
		return
	}
	if err := p.Gateway.DeleteProvider(id); err != nil {
		p.ProviderNotice = err.Error()
		return
	}
	delete(p.enabled, id)
	p.DeletingID = ""
	p.ProviderNotice = ""
	if p.FormID == id {
		p.closeForm()
	}
	p.reloadRoutes()
	p.resetChatTarget()
	p.repaint()
}

func (p *Page) startTest(id string) {
	if p.Gateway == nil || p.TestingID != "" {
		return
	}
	p.TestingID = id
	p.ProviderNotice = ""
	go p.TestProvider(context.Background(), id)
}

// TestProvider runs a connection test and applies the result on the main
// thread. The view calls it in a goroutine; tests can call it directly.
func (p *Page) TestProvider(ctx context.Context, id string) (gateway.TestResult, error) {
	result, err := p.Gateway.TestProvider(ctx, id)
	p.dispatch(func() {
		p.TestingID = ""
		if err != nil {
			p.ProviderNotice = gateway.KindText(gateway.KindOf(err)) + " · " + err.Error()
		} else {
			p.ProviderNotice = "测试成功 · " + result.Message
		}
		p.reloadRoutes()
		p.resetChatTarget()
		p.repaint()
	})
	return result, err
}

func (p *Page) reloadRoutes() {
	if p.Gateway == nil {
		return
	}
	for _, cap := range gateway.EditableCapabilities {
		st := p.Routes[cap]
		if st == nil {
			st = &routeState{}
			p.Routes[cap] = st
		}
		views := p.Gateway.RouteTargets(cap)
		st.draft = make([]gateway.Route, 0, len(views))
		for _, v := range views {
			st.draft = append(st.draft, gateway.Route{Provider: v.ProviderID, Model: v.Model})
		}
		st.add = addTargetDefault
		p.syncPrimary(cap)
	}
}

func (p *Page) syncPrimary(cap gateway.Capability) {
	st := p.Routes[cap]
	if st == nil {
		return
	}
	if len(st.draft) == 0 {
		st.primary = noneTarget
		return
	}
	st.primary = p.routeLabel(st.draft[0])
}

func (p *Page) routeLabel(r gateway.Route) string {
	for _, entry := range p.entries() {
		if entry.Provider.ID == r.Provider {
			return entry.Provider.Name + " · " + r.Model
		}
	}
	return r.Provider + " · " + r.Model
}

func (p *Page) findTarget(label string) (gateway.TargetView, bool) {
	if p.Gateway == nil {
		return gateway.TargetView{}, false
	}
	for _, option := range p.Gateway.TargetOptions() {
		if option.Label() == label {
			return option, true
		}
	}
	for _, cap := range gateway.EditableCapabilities {
		for _, view := range p.Gateway.RouteTargets(cap) {
			if view.Label() == label {
				return view, true
			}
		}
	}
	return gateway.TargetView{}, false
}

func (p *Page) targetLabels(extra ...string) []string {
	labels := []string{noneTarget}
	seen := map[string]bool{noneTarget: true}
	add := func(label string) {
		if !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	if p.Gateway != nil {
		for _, option := range p.Gateway.TargetOptions() {
			add(option.Label())
		}
	}
	for _, label := range extra {
		if label != "" {
			add(label)
		}
	}
	return labels
}

func (p *Page) applyPrimary(cap gateway.Capability) {
	st := p.Routes[cap]
	if st == nil {
		return
	}
	chosen := st.primary
	var rest []gateway.Route
	if len(st.draft) > 0 {
		rest = st.draft[1:]
	}
	if chosen == noneTarget || chosen == "" {
		st.draft = rest
		return
	}
	target, ok := p.findTarget(chosen)
	if !ok {
		p.syncPrimary(cap)
		return
	}
	key := target.RouteKey()
	kept := make([]gateway.Route, 0, len(rest))
	for _, r := range rest {
		if r.Provider+"|"+r.Model != key {
			kept = append(kept, r)
		}
	}
	st.draft = append([]gateway.Route{{Provider: target.ProviderID, Model: target.Model}}, kept...)
	p.syncPrimary(cap)
}

func (p *Page) routesView(c *mygoui.Context, t *mygoui.Theme) {
	card := mygoui.Column(c).FillWidth().Padding(16).Gap(12).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		mygoui.Text(c, "能力路由").FontSize(15).Bold().TextColor(t.Text)
		mygoui.Text(c, "主目标优先调用，失败时按顺序回退；停用的供应商不参与调用。").
			FontSize(12).TextColor(t.TextMuted).Wrap()
		for _, cap := range gateway.EditableCapabilities {
			p.routeEditor(c, t, cap)
		}
	})
}

func (p *Page) routeEditor(c *mygoui.Context, t *mygoui.Theme, cap gateway.Capability) {
	st := p.Routes[cap]
	if st == nil {
		return
	}
	title := "对话（chat）"
	if cap == gateway.CapabilityEmbedding {
		title = "向量（embedding）"
	}
	block := mygoui.Column(c).FillWidth().Gap(8).Padding(10).Radius(t.Radius).
		Background(t.Background).BorderWidth(1).BorderColor(t.Border)
	block.Children(func() {
		mygoui.Text(c, title).FontSize(13).Bold().TextColor(t.Text)
		primary := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
		primary.Children(func() {
			mygoui.Text(c, "主目标").FontSize(12).TextColor(t.TextMuted)
			sel := mygoui.Select(c, &st.primary, p.targetLabels()).Width(280).
				Label("主目标 " + title)
			if sel.Changed() {
				p.applyPrimary(cap)
			}
		})
		for i := 1; i < len(st.draft); i++ {
			index := i
			backup := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
			backup.Children(func() {
				mygoui.Text(c, "备用 "+p.routeLabel(st.draft[index])).
					FontSize(12).TextColor(t.TextMuted).SingleLine()
				mygoui.Spacer(c)
				p.button(c, t, "移除", false, false, func() {
					st.draft = append(st.draft[:index], st.draft[index+1:]...)
					p.syncPrimary(cap)
				})
			})
		}
		addRow := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
		addRow.Children(func() {
			mygoui.Text(c, "备用目标").FontSize(12).TextColor(t.TextMuted)
			options := p.addOptions(st)
			if st.add == "" || !containsLabel(options, st.add) {
				st.add = addTargetDefault
			}
			mygoui.Select(c, &st.add, options).Width(280).Label("要添加的备用目标 " + title)
			p.button(c, t, "添加备用", false, len(options) <= 1, func() { p.addBackup(cap) })
			mygoui.Spacer(c)
			p.button(c, t, "保存路由", true, false, func() { p.saveRoutes(cap) })
		})
		if st.notice != "" {
			mygoui.Text(c, st.notice).FontSize(11).
				TextColor(ui.DangerColor(p.mode(t))).Wrap()
		}
	})
}

func (p *Page) addOptions(st *routeState) []string {
	options := []string{addTargetDefault}
	for _, label := range p.targetLabels() {
		if label == noneTarget {
			continue
		}
		target, ok := p.findTarget(label)
		if !ok || routeUsed(st.draft, target) {
			continue
		}
		options = append(options, label)
	}
	return options
}

func routeUsed(routes []gateway.Route, target gateway.TargetView) bool {
	for _, r := range routes {
		if r.Provider == target.ProviderID && r.Model == target.Model {
			return true
		}
	}
	return false
}

func containsLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

func (p *Page) addBackup(cap gateway.Capability) {
	st := p.Routes[cap]
	if st == nil || st.add == "" || st.add == addTargetDefault {
		return
	}
	target, ok := p.findTarget(st.add)
	if !ok {
		return
	}
	st.draft = append(st.draft, gateway.Route{Provider: target.ProviderID, Model: target.Model})
	st.add = addTargetDefault
	p.syncPrimary(cap)
}

func (p *Page) saveRoutes(cap gateway.Capability) {
	st := p.Routes[cap]
	if st == nil || p.Gateway == nil {
		return
	}
	if err := p.Gateway.SaveRoutes(cap, st.draft); err != nil {
		st.notice = validationText(err)
		return
	}
	p.reloadRoutes()
	if p.Routes[cap] != nil {
		p.Routes[cap].notice = "路由已保存"
	}
	p.resetChatTarget()
	p.repaint()
}

func (p *Page) chatTest(c *mygoui.Context, t *mygoui.Theme) {
	card := mygoui.Column(c).FillWidth().Padding(16).Gap(10).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		mygoui.Text(c, "对话测试").FontSize(15).Bold().TextColor(t.Text)
		mygoui.Text(c, "向已保存的对话目标发送一条消息，验证网关可用。").
			FontSize(12).TextColor(t.TextMuted).Wrap()
		controls := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
		controls.Children(func() {
			labels := p.targetLabels()
			if len(labels) <= 1 {
				p.ChatTarget = noneTarget
			} else if !containsLabel(labels, p.ChatTarget) {
				p.resetChatTarget()
			}
			mygoui.Select(c, &p.ChatTarget, labels).Width(260).Label("测试目标")
			mygoui.TextInput(c, &p.Prompt).Width(360).Label("提示词").
				Placeholder("输入提示词")
			disabled := p.ChatSending || strings.TrimSpace(p.Prompt) == "" ||
				p.ChatTarget == noneTarget
			label := "发送"
			if p.ChatSending {
				label = "发送中…"
			}
			p.button(c, t, label, true, disabled, p.startChat)
		})
		if len(p.targetLabels()) <= 1 {
			mygoui.Text(c, "模型未配置：请先在能力路由中设置对话主目标。").
				FontSize(12).TextColor(t.TextMuted).Wrap()
		}
		if p.ChatSending {
			mygoui.Text(c, "正在请求模型…").FontSize(12).TextColor(t.TextMuted)
		}
		if p.ChatError != "" {
			mygoui.Text(c, p.ChatError).FontSize(12).
				TextColor(ui.DangerColor(p.mode(t))).Wrap()
		}
		if p.ChatReply != nil {
			resp := p.ChatReply
			tokens := "token 用量未返回"
			if resp.HasUsage {
				tokens = fmt.Sprintf("输入 %d · 输出 %d token", resp.InputTokens, resp.OutputTokens)
			}
			meta := fmt.Sprintf("%s · %s · %s", resp.Model,
				resp.Duration.Round(10*time.Millisecond), tokens)
			mygoui.Text(c, meta).FontSize(11).TextColor(t.TextMuted).SingleLine()
			reply := resp.Content
			if strings.TrimSpace(reply) == "" {
				reply = "（空回复）"
			}
			mygoui.Text(c, reply).FontSize(13).TextColor(t.Text).Wrap()
		}
		if p.ChatReply == nil && p.ChatError == "" && !p.ChatSending {
			mygoui.Text(c, "回复与用量会显示在这里").FontSize(11).TextColor(t.TextMuted)
		}
	})
}

func (p *Page) resetChatTarget() {
	if p.Gateway == nil {
		p.ChatTarget = noneTarget
		return
	}
	views := p.Gateway.RouteTargets(gateway.CapabilityChat)
	if len(views) == 0 {
		p.ChatTarget = noneTarget
		return
	}
	p.ChatTarget = views[0].Label()
}

func (p *Page) startChat() {
	if p.Gateway == nil || p.ChatSending {
		return
	}
	target, ok := p.findTarget(p.ChatTarget)
	if !ok {
		p.ChatError = "请选择一个可用的测试目标"
		return
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return
	}
	p.ChatSending = true
	p.ChatError = ""
	p.ChatReply = nil
	go func() {
		req := gateway.ChatRequest{
			Messages:  []gateway.ChatMessage{{Role: "user", Content: strings.TrimSpace(p.Prompt)}},
			MaxTokens: ChatTestMaxTokens,
		}
		p.SendChat(context.Background(), target, req)
	}()
}

// SendChat calls one target and applies the result on the main thread. The
// view calls it in a goroutine; tests can call it directly.
func (p *Page) SendChat(ctx context.Context, target gateway.TargetView, req gateway.ChatRequest) (gateway.ChatResponse, error) {
	resp, err := p.Gateway.ChatProvider(ctx, target.ProviderID, target.Model, req, OriginSettings)
	p.dispatch(func() {
		p.ChatSending = false
		if err != nil {
			p.ChatReply = nil
			p.ChatError = gateway.KindText(gateway.KindOf(err)) + " · " + err.Error()
		} else {
			p.ChatError = ""
			p.ChatReply = &resp
		}
		p.repaint()
	})
	return resp, err
}

func (p *Page) readOnly(c *mygoui.Context, t *mygoui.Theme) {
	card := mygoui.Column(c).FillWidth().Padding(16).Gap(6).Radius(t.Radius).
		Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	card.Children(func() {
		mygoui.Text(c, "其他能力").FontSize(15).Bold().TextColor(t.Text)
		mygoui.Text(c, "图片、视频、语音合成与语音识别：随对应模块接入后开放。").
			FontSize(12).TextColor(t.TextMuted).Wrap()
	})
}

func (p *Page) mode(t *mygoui.Theme) ui.Mode {
	if t.Dark {
		return ui.ModeDark
	}
	return ui.ModeLight
}

func (p *Page) button(c *mygoui.Context, t *mygoui.Theme, label string, primary, disabled bool, action func()) {
	p.buttonLabeled(c, t, label, label, primary, disabled, action)
}

func (p *Page) buttonLabeled(c *mygoui.Context, t *mygoui.Theme, label, accessible string, primary, disabled bool, action func()) {
	b := mygoui.ButtonBase(c).Label(accessible).Shrink(0).
		Padding(5, 10).Radius(t.Radius).Disabled(disabled)
	if disabled {
		b.Background(t.SurfacePressed)
	} else if primary {
		b.Background(t.Accent)
	} else {
		b.Background(t.SurfacePressed)
		if b.Pressed() {
			b.Background(t.SurfaceHover)
		} else if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
	}
	b.Children(func() {
		color := t.Text
		if primary && !disabled {
			color = t.AccentText
		}
		if disabled {
			color = t.TextMuted
		}
		mygoui.Text(c, label).FontSize(12).TextColor(color)
	})
	if b.Clicked() && !disabled {
		action()
	}
}

func (p *Page) dispatch(fn func()) {
	if p.Dispatch != nil {
		p.Dispatch(fn)
		return
	}
	mygo.RunOnMain(fn)
}

func (p *Page) repaint() {
	if p.Repaint != nil {
		p.Repaint()
	}
}
