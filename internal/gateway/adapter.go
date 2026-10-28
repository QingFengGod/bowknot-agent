package gateway

import (
	"context"
	"time"
)

// ChatMessage is one message of a chat request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is a chat completion request. Model is filled by the router;
// OnDelta receives streamed content when Stream is set. ConversationID is an
// optional local hint written to the usage log. ProviderID and Model, when
// set, pin the call to one route target instead of trying the route in order.
type ChatRequest struct {
	ProviderID     string
	Model          string
	ConversationID string
	Messages       []ChatMessage
	MaxTokens      int
	Temperature    *float64
	Stream         bool
	OnDelta        func(string)
}

// ChatResponse is a completed chat call.
type ChatResponse struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
	HasUsage     bool
	Duration     time.Duration
	ProviderID   string
}

// EmbedRequest asks for vectors of one or more texts.
type EmbedRequest struct {
	Model string
	Input []string
}

// EmbedResponse holds the vectors and the token use of an embedding call.
type EmbedResponse struct {
	Vectors     [][]float32
	Model       string
	InputTokens int
	HasUsage    bool
	Duration    time.Duration
	ProviderID  string
}

// Adapter is one provider protocol implementation.
type Adapter interface {
	Type() string
	Test(ctx context.Context, p Provider, key string) (TestResult, []string, error)
	Chat(ctx context.Context, p Provider, key string, req ChatRequest) (ChatResponse, error)
	Embed(ctx context.Context, p Provider, key string, req EmbedRequest) (EmbedResponse, error)
}

// AdapterRegistry maps a provider type to its adapter.
type AdapterRegistry struct {
	adapters map[string]Adapter
}

// NewAdapterRegistry returns an empty registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: map[string]Adapter{}}
}

// Register adds an adapter, replacing one of the same type.
func (r *AdapterRegistry) Register(a Adapter) {
	r.adapters[a.Type()] = a
}

// Get returns the adapter of a provider type.
func (r *AdapterRegistry) Get(typ string) (Adapter, bool) {
	a, ok := r.adapters[typ]
	return a, ok
}
