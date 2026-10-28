// Package agent 实现主 Agent 的会话数据、对话执行与助手页。
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 会话文件的结构与上限。schema 变更时必须递增并处理旧版本。
const (
	// SchemaVersion is the conversation file schema this build reads.
	SchemaVersion = 1
	// MaxMessages caps the messages of one conversation.
	MaxMessages = 500
	// MaxConversationBytes is the largest conversation file the store loads.
	MaxConversationBytes = 4 << 20
	// DefaultTitle is the title of a new conversation.
	DefaultTitle = "新会话"
)

// titleRunes is how many runes of the first user message become the title.
const titleRunes = 30

// Role is the author of a message.
type Role string

// Message roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// MessageStatus is the outcome of an assistant reply.
type MessageStatus string

// Message statuses. StatusStreaming exists only in memory while a reply
// streams; it is never written to disk.
const (
	StatusOK        MessageStatus = "ok"
	StatusError     MessageStatus = "error"
	StatusAborted   MessageStatus = "aborted"
	StatusStreaming MessageStatus = "streaming"
)

// Message is one turn of a conversation. The usage fields are filled from the
// provider response; a failure keeps whatever content had streamed.
type Message struct {
	Role           Role          `json:"role"`
	Content        string        `json:"content"`
	Model          string        `json:"model,omitempty"`
	InputTokens    int           `json:"input_tokens,omitempty"`
	OutputTokens   int           `json:"output_tokens,omitempty"`
	UsageEstimated bool          `json:"usage_estimated"`
	DurationMS     int64         `json:"duration_ms,omitempty"`
	Status         MessageStatus `json:"status"`
	ErrorCode      string        `json:"error_code,omitempty"`
	ErrorMessage   string        `json:"error_message,omitempty"`
	At             int64         `json:"at"`
}

// Conversation is one local chat session, stored as one JSON file.
type Conversation struct {
	Schema      int       `json:"schema"`
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	TitleEdited bool      `json:"title_edited,omitempty"`
	CreatedAt   int64     `json:"created_at"`
	UpdatedAt   int64     `json:"updated_at"`
	Model       string    `json:"model,omitempty"`
	Messages    []Message `json:"messages"`
}

// NewConversation returns an empty conversation created at now.
func NewConversation(now time.Time) *Conversation {
	at := now.Unix()
	return &Conversation{
		Schema:    SchemaVersion,
		ID:        NewConversationID(),
		Title:     DefaultTitle,
		CreatedAt: at,
		UpdatedAt: at,
	}
}

// NewConversationID returns a conversation ID of the form c-xxxxxxxx.
func NewConversationID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand only fails when the system source is broken; fall back
		// to the clock so that creating a conversation never blocks the UI.
		return fmt.Sprintf("c-%08x", time.Now().UnixNano()&0xffffffff)
	}
	return "c-" + hex.EncodeToString(buf[:])
}

// TitleFrom derives a conversation title from the first user message: the
// whitespace is collapsed and the result is cut to 30 runes.
func TitleFrom(content string) string {
	joined := strings.Join(strings.Fields(content), " ")
	if joined == "" {
		return DefaultTitle
	}
	runes := []rune(joined)
	if len(runes) > titleRunes {
		runes = runes[:titleRunes]
	}
	return string(runes)
}

// Clone returns a deep copy of c.
func (c Conversation) Clone() Conversation {
	out := c
	out.Messages = append([]Message(nil), c.Messages...)
	return out
}

// Validate checks the fields read from disk. A file that fails is skipped by
// the store and never deleted.
func (c *Conversation) Validate() error {
	if c.Schema != SchemaVersion {
		return fmt.Errorf("schema %d 不受支持", c.Schema)
	}
	if !ValidConversationID(c.ID) {
		return errors.New("会话 ID 非法")
	}
	if c.CreatedAt <= 0 || c.UpdatedAt <= 0 {
		return errors.New("时间戳非法")
	}
	if len(c.Messages) > MaxMessages {
		return fmt.Errorf("消息 %d 条，超过上限 %d", len(c.Messages), MaxMessages)
	}
	for i := range c.Messages {
		if err := c.Messages[i].Validate(); err != nil {
			return fmt.Errorf("消息 %d：%w", i+1, err)
		}
	}
	return nil
}

// Validate checks one message read from disk. Streaming replies are not a
// persisted status.
func (m Message) Validate() error {
	switch m.Role {
	case RoleUser, RoleAssistant:
	default:
		return fmt.Errorf("角色 %q 非法", m.Role)
	}
	switch m.Status {
	case StatusOK, StatusError, StatusAborted:
	default:
		return fmt.Errorf("状态 %q 非法", m.Status)
	}
	if m.At <= 0 {
		return errors.New("时间戳非法")
	}
	return nil
}

// ValidConversationID reports whether id is safe to use as a file name: the
// c- prefix followed by eight lowercase hexadecimal digits.
func ValidConversationID(id string) bool {
	if len(id) != 10 || !strings.HasPrefix(id, "c-") {
		return false
	}
	for _, r := range id[2:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// TitleText returns the title shown in lists, defaulting an empty one.
func (c Conversation) TitleText() string {
	if strings.TrimSpace(c.Title) == "" {
		return DefaultTitle
	}
	return c.Title
}

// TotalTokens returns the sum of input and output tokens of the replies.
func (c Conversation) TotalTokens() (input, output int) {
	for _, m := range c.Messages {
		input += m.InputTokens
		output += m.OutputTokens
	}
	return input, output
}

// LastAssistant returns the last message when it is an assistant reply.
func (c Conversation) LastAssistant() (Message, bool) {
	if len(c.Messages) == 0 {
		return Message{}, false
	}
	last := c.Messages[len(c.Messages)-1]
	return last, last.Role == RoleAssistant
}
