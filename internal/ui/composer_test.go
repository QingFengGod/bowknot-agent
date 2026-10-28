package ui

import (
	"strings"
	"testing"

	mygoui "github.com/egoist/mygo/ui"
)

func newComposerTester(t *testing.T, m Composer) *mygoui.Tester {
	t.Helper()
	return mygoui.NewTester(func(c *mygoui.Context) {
		mygoui.Column(c).FillWidth().Padding(12).Children(func() {
			ComposerView(c, c.Theme(), m)
		})
	}, 640, 280)
}

func TestComposerEnterSends(t *testing.T) {
	draft, model := "", "A · m1"
	var sent []string
	m := Composer{
		Value: &draft, Placeholder: "输入消息",
		Models: []string{model}, Model: &model,
		OnSubmit: func(text string) bool { sent = append(sent, text); return true },
	}
	tester := newComposerTester(t, m)
	tester.Key(0, mygoui.KeyTab)
	tester.Type("  你好  ")
	tester.Key(0, mygoui.KeyEnter)
	if len(sent) != 1 || sent[0] != "你好" {
		t.Fatalf("submitted %q, want the trimmed text once", sent)
	}
	if draft != "" {
		t.Errorf("draft = %q, want it cleared after sending", draft)
	}
}

func TestComposerShiftEnterBreaksLine(t *testing.T) {
	draft := ""
	var sent []string
	m := Composer{
		Value:    &draft,
		OnSubmit: func(text string) bool { sent = append(sent, text); return true },
	}
	tester := newComposerTester(t, m)
	tester.Key(0, mygoui.KeyTab)
	tester.Type("第一行")
	tester.Key(mygoui.Shift, mygoui.KeyEnter)
	tester.Type("第二行")
	if len(sent) != 0 {
		t.Errorf("Shift+Enter submitted %q", sent)
	}
	if !strings.Contains(draft, "\n") {
		t.Errorf("draft = %q, want a line break", draft)
	}
	if !strings.Contains(draft, "第二行") {
		t.Errorf("draft = %q, want the second line", draft)
	}
}

func TestComposerBusyShowsStop(t *testing.T) {
	draft := "你好"
	stopped, sent := 0, 0
	m := Composer{
		Value:  &draft,
		Busy:   true,
		OnStop: func() { stopped++ },
		OnSend: func() { sent++ },
	}
	tester := newComposerTester(t, m)
	tester.Frame()
	if tester.HasText("发送") {
		t.Error("busy composer still offers the send button")
	}
	if !tester.HasText("停止") {
		t.Fatal("busy composer lacks the stop button")
	}
	if err := tester.Click("停止"); err != nil {
		t.Fatal(err)
	}
	if stopped != 1 || sent != 0 {
		t.Errorf("stopped = %d, sent = %d, want one stop and no send", stopped, sent)
	}
}

func TestComposerEmptyDraftCannotSend(t *testing.T) {
	draft := ""
	sent := 0
	m := Composer{
		Value:  &draft,
		OnSend: func() { sent++ },
	}
	tester := newComposerTester(t, m)
	tester.Frame()
	if err := tester.Click("发送"); err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Error("an empty draft sent")
	}
}

func TestComposerAttachAndHint(t *testing.T) {
	draft := ""
	attached := 0
	m := Composer{
		Value:    &draft,
		Hint:     "网关离线，恢复连接后可启用对话",
		Disabled: true,
		OnAttach: func() { attached++ },
	}
	tester := newComposerTester(t, m)
	tester.Frame()
	if !tester.HasText("网关离线") {
		t.Error("view does not show the hint")
	}
	if err := tester.Click("添加附件"); err != nil {
		t.Fatal(err)
	}
	if attached != 1 {
		t.Errorf("attached = %d, want the attach callback once", attached)
	}
}

func TestComposerModelPicker(t *testing.T) {
	draft, model := "", "A · m1"
	m := Composer{
		Value:  &draft,
		Models: []string{"A · m1", "B · m2"},
		Model:  &model,
	}
	tester := newComposerTester(t, m)
	tester.Frame()
	if err := tester.Click("A · m1"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("B · m2"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if model != "B · m2" {
		t.Errorf("model = %q, want the picked one", model)
	}
}
