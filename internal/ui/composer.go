package ui

import (
	"strings"

	mygoui "github.com/egoist/mygo/ui"
)

// Metrics of the composer.
const (
	composerRadius     = 16
	composerButtonSize = 32
	composerIconSize   = 16
	composerAttachSize = 28
	composerSelectW    = 170
)

// Composer is a chat message input: a growing text area in a rounded box,
// with the attach slot, the model picker and the send / stop button below it.
type Composer struct {
	// Value is the draft being edited.
	Value *string
	// Placeholder shows while the draft is empty.
	Placeholder string
	// Models are the chat targets to choose from; Model takes the choice.
	Models []string
	Model  *string
	// ModelFixed shows a bound target instead of the picker, as a sub agent
	// conversation whose model comes from its own settings.
	ModelFixed string
	// Hint explains a blocked or failed send, beside the controls.
	Hint string
	// Busy is whether a reply is being generated; the send button becomes
	// the stop button while it is.
	Busy bool
	// Disabled turns off sending, such as while the gateway is offline.
	Disabled bool
	// OnSend starts a reply, and OnStop aborts the running one.
	OnSend func()
	OnStop func()
	// OnSubmit starts a reply for the text typed when Enter is pressed. It
	// reports whether the composer clears the draft.
	OnSubmit func(text string) bool
	// OnAttach opens the file picker; a nil value grays out the attach slot.
	OnAttach func()
	// TaskMode is whether the draft is sent as a task plan.
	TaskMode bool
	// TaskModeDisabled turns off the task mode tag, such as when no sub agent
	// can accept work.
	TaskModeDisabled bool
	// OnToggleTaskMode flips the task mode; a nil value hides the tag.
	OnToggleTaskMode func()
}

// ComposerView draws the composer. Enter sends, Shift+Enter breaks the line.
func ComposerView(c *mygoui.Context, t *mygoui.Theme, m Composer) mygoui.Element {
	box := mygoui.Column(c).FillWidth().Shrink(0).Padding(10, 12).Gap(6).
		Radius(composerRadius).Background(t.Surface).BorderWidth(1).BorderColor(t.Border)
	box.Children(func() {
		area := mygoui.TextAreaBase(c, m.Value).FillWidth().Lines(1, 8).
			Placeholder(m.Placeholder).FontSize(13).TextColor(t.Text)
		if m.Disabled {
			area.Disabled(true)
		}
		area.OnTextKey(func(k mygoui.TextKey) bool {
			if k.Key != mygoui.KeyEnter || k.Mods != 0 {
				return false // Shift+Enter breaks the line
			}
			if m.Busy || m.Disabled || m.OnSubmit == nil {
				return true
			}
			text := strings.TrimSpace(k.Text())
			if text == "" || !m.OnSubmit(text) {
				return true
			}
			k.SetText("")
			return true
		})
		row := mygoui.Row(c).FillWidth().Shrink(0).Gap(6).AlignItems(mygoui.Center)
		row.Children(func() {
			attachButton(c, t, m)
			taskModeTag(c, t, m)
			if m.Hint != "" {
				mygoui.Text(c, m.Hint).FontSize(10).TextColor(t.TextMuted).
					SingleLine().Grow(1)
			} else {
				mygoui.Spacer(c).Grow(1)
			}
			modelPicker(c, t, m)
			sendButton(c, t, m)
		})
	})
	return box
}

// attachButton is the reserved slot for attaching files.
func attachButton(c *mygoui.Context, t *mygoui.Theme, m Composer) {
	b := mygoui.ButtonBase(c).Shrink(0).Label("添加附件").
		Size(composerAttachSize, composerAttachSize).Radius(composerAttachSize / 2).
		Center().TextColor(t.TextMuted)
	if m.OnAttach == nil {
		b.Disabled(true)
	} else if b.Pressed() {
		b.Background(t.SurfacePressed)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		mygoui.Icon(c, AttachIcon).Size(composerIconSize, composerIconSize)
	})
	if b.Clicked() && m.OnAttach != nil {
		m.OnAttach()
	}
}

// taskModeTag flips the draft between a chat and a task plan. It is an
// outlined chip while on and muted text while off.
func taskModeTag(c *mygoui.Context, t *mygoui.Theme, m Composer) {
	if m.OnToggleTaskMode == nil {
		return
	}
	b := mygoui.ButtonBase(c).Shrink(0).Label("任务模式").
		Padding(3, 8).Radius(composerRadius / 2).
		BorderWidth(1).BorderColor(t.Border).Center()
	if m.TaskModeDisabled {
		b.Disabled(true)
	} else if m.TaskMode {
		b.BorderColor(t.Accent)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		color := t.TextMuted
		if m.TaskMode && !m.TaskModeDisabled {
			color = t.Accent
		}
		mygoui.Text(c, "任务模式").FontSize(10).TextColor(color)
	})
	if b.Clicked() && !m.TaskModeDisabled {
		m.OnToggleTaskMode()
	}
}

// modelPicker switches the chat target beside the send button, or shows the
// bound target of a sub agent.
func modelPicker(c *mygoui.Context, t *mygoui.Theme, m Composer) {
	if m.ModelFixed != "" {
		mygoui.Text(c, m.ModelFixed).FontSize(11).TextColor(t.TextMuted).
			Shrink(0).SingleLine()
		return
	}
	if len(m.Models) == 0 || m.Model == nil {
		mygoui.Text(c, "模型未配置").FontSize(11).TextColor(t.TextMuted).Shrink(0).SingleLine()
		return
	}
	mygoui.Select(c, m.Model, m.Models).Width(composerSelectW).Shrink(0)
}

// sendButton sends the draft, or stops the running reply while busy.
func sendButton(c *mygoui.Context, t *mygoui.Theme, m Composer) {
	icon := SendIcon
	label := "发送"
	enabled := !m.Disabled && m.OnSend != nil
	if m.Busy {
		icon, label = StopIcon, "停止"
		enabled = m.OnStop != nil
	} else if m.Value == nil || strings.TrimSpace(*m.Value) == "" {
		enabled = false
	}
	b := mygoui.ButtonBase(c).Shrink(0).Label(label).
		Size(composerButtonSize, composerButtonSize).Radius(composerButtonSize / 2).
		Center().TextColor(t.AccentText)
	if !enabled {
		b.Disabled(true)
		b.Background(t.SurfaceHover)
		b.TextColor(t.TextMuted)
	} else {
		b.Background(t.Accent)
		if b.Pressed() {
			b.Background(t.AccentPressed)
		} else if b.Hovered() {
			b.Background(t.AccentHover)
		}
	}
	b.Children(func() {
		mygoui.Icon(c, icon).Size(composerIconSize, composerIconSize)
	})
	if b.Clicked() && enabled {
		if m.Busy {
			m.OnStop()
		} else {
			m.OnSend()
		}
	}
}
