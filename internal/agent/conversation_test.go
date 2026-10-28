package agent

import (
	"strings"
	"testing"
	"time"
)

func TestTitleFrom(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "   \n\t ", DefaultTitle},
		{"plain", "你好", "你好"},
		{"collapses whitespace", "帮我  总结\n这段文字", "帮我 总结 这段文字"},
		{"keeps 30 runes", strings.Repeat("字", 40), strings.Repeat("字", 30)},
		{"trims around", "  问题  ", "问题"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TitleFrom(tc.in); got != tc.want {
				t.Errorf("TitleFrom(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewConversationDefaults(t *testing.T) {
	conv := NewConversation(time.Unix(1790000000, 0))
	if conv.Schema != SchemaVersion || conv.Title != DefaultTitle {
		t.Errorf("conversation = %+v", conv)
	}
	if conv.CreatedAt != 1790000000 || conv.UpdatedAt != conv.CreatedAt {
		t.Errorf("times = %d,%d", conv.CreatedAt, conv.UpdatedAt)
	}
	if !ValidConversationID(conv.ID) {
		t.Errorf("ID = %q", conv.ID)
	}
	if err := conv.Validate(); err != nil {
		t.Errorf("new conversation invalid: %v", err)
	}
}

func TestValidConversationID(t *testing.T) {
	cases := map[string]bool{
		"c-1a2b3c4d":  true,
		"c-00000000":  true,
		"c-1A2B3C4D":  false,
		"x-1a2b3c4d":  false,
		"c-1a2b3c4":   false,
		"c-1a2b3c4de": false,
		"c-../../x":   false,
		"":            false,
	}
	for id, want := range cases {
		if got := ValidConversationID(id); got != want {
			t.Errorf("ValidConversationID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestConversationValidate(t *testing.T) {
	base := func() Conversation {
		return Conversation{
			Schema: SchemaVersion, ID: "c-1a2b3c4d", Title: "t",
			CreatedAt: 1, UpdatedAt: 2,
			Messages: []Message{{Role: RoleUser, Content: "hi", Status: StatusOK, At: 1}},
		}
	}
	cases := []struct {
		name   string
		mutate func(*Conversation)
		ok     bool
	}{
		{"valid", func(c *Conversation) {}, true},
		{"schema", func(c *Conversation) { c.Schema = 2 }, false},
		{"id", func(c *Conversation) { c.ID = "../x" }, false},
		{"created", func(c *Conversation) { c.CreatedAt = 0 }, false},
		{"role", func(c *Conversation) { c.Messages[0].Role = "system" }, false},
		{"status", func(c *Conversation) { c.Messages[0].Status = StatusStreaming }, false},
		{"time", func(c *Conversation) { c.Messages[0].At = 0 }, false},
		{"too many", func(c *Conversation) {
			c.Messages = make([]Message, MaxMessages+1)
			for i := range c.Messages {
				c.Messages[i] = Message{Role: RoleUser, Status: StatusOK, At: 1}
			}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conv := base()
			tc.mutate(&conv)
			err := conv.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !tc.ok && err == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}

func TestConversationTotals(t *testing.T) {
	conv := Conversation{Messages: []Message{
		{Role: RoleUser, Status: StatusOK, At: 1},
		{Role: RoleAssistant, Status: StatusOK, InputTokens: 12, OutputTokens: 34, At: 2},
		{Role: RoleAssistant, Status: StatusError, InputTokens: 1, OutputTokens: 2, At: 3},
	}}
	input, output := conv.TotalTokens()
	if input != 13 || output != 36 {
		t.Errorf("tokens = %d,%d, want 13,36", input, output)
	}
	last, ok := conv.LastAssistant()
	if !ok || last.At != 3 {
		t.Errorf("LastAssistant() = %+v,%v", last, ok)
	}
}

func TestConversationClone(t *testing.T) {
	conv := NewConversation(time.Unix(1, 0))
	conv.Messages = []Message{{Role: RoleUser, Content: "a", Status: StatusOK, At: 1}}
	clone := conv.Clone()
	clone.Messages[0].Content = "b"
	if conv.Messages[0].Content != "a" {
		t.Error("Clone shares the message slice")
	}
}
