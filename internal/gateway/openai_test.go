package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// roundTripFunc lets a test answer HTTP requests without a real socket; the
// sandbox this project builds in refuses to bind a loopback listener.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testProvider(baseURL string) Provider {
	return Provider{ID: "p-00000001", Name: "Test", Type: ProviderOpenAI,
		BaseURL: baseURL + "/v1", Enabled: true}
}

func TestOpenAITestSuccess(t *testing.T) {
	client := testClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		return jsonResponse(http.StatusOK, `{"data":[{"id":"m1"},{"id":"m2"},{"id":"m1"},{"id":""}]}`), nil
	})
	adapter := NewOpenAIAdapter()
	adapter.Client = client

	result, models, err := adapter.Test(context.Background(), testProvider("http://test.local"), "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Kind != TestKindModels {
		t.Errorf("result = %+v", result)
	}
	if got := strings.Join(models, ","); got != "m1,m2" {
		t.Errorf("models = %q, want m1,m2", got)
	}
	if !strings.Contains(result.Message, "2") {
		t.Errorf("message = %q, want the model count", result.Message)
	}
}

func TestOpenAITestStatusKinds(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   ErrorKind
	}{
		{401, `{"error":"bad key"}`, KindAuth},
		{429, `{"error":"limited"}`, KindQuota},
		{500, `{"error":"boom"}`, KindServer},
		{400, `{"error":{"code":"content_filter"}}`, KindModeration},
	}
	for _, tc := range tests {
		adapter := NewOpenAIAdapter()
		adapter.Client = testClient(func(*http.Request) (*http.Response, error) {
			return jsonResponse(tc.status, tc.body), nil
		})
		_, _, err := adapter.Test(context.Background(), testProvider("http://test.local"), "")
		if got := KindOf(err); got != tc.want {
			t.Errorf("status %d kind = %s, want %s", tc.status, got, tc.want)
		}
	}
}

func TestOpenAITestTimeout(t *testing.T) {
	adapter := NewOpenAIAdapter()
	adapter.TestTimeout = 20 * time.Millisecond
	adapter.Client = testClient(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	_, _, err := adapter.Test(context.Background(), testProvider("http://test.local"), "")
	if got := KindOf(err); got != KindTimeout {
		t.Errorf("kind = %s, want %s (%v)", got, KindTimeout, err)
	}
}

func TestOpenAIChatNonStream(t *testing.T) {
	var received chatBody
	adapter := NewOpenAIAdapter()
	adapter.Client = testClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		return jsonResponse(http.StatusOK,
			`{"model":"m1-2024","choices":[{"message":{"content":"你好"}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`), nil
	})

	resp, err := adapter.Chat(context.Background(), testProvider("http://test.local"), "sk-test", ChatRequest{
		Model:    "m1",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "你好" || resp.Model != "m1-2024" {
		t.Errorf("resp = %+v", resp)
	}
	if !resp.HasUsage || resp.InputTokens != 5 || resp.OutputTokens != 2 {
		t.Errorf("usage = %+v", resp)
	}
	if received.Model != "m1" || received.Stream {
		t.Errorf("request = %+v", received)
	}
}

func TestOpenAIChatStream(t *testing.T) {
	stream := strings.Join([]string{
		`: keep-alive`,
		`data: {"model":"m1","choices":[{"delta":{"content":"你"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"好"}}]}`,
		`data: [DONE]`,
		``,
	}, "\n")
	adapter := NewOpenAIAdapter()
	adapter.Client = testClient(func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusOK, stream)
		response.Header.Set("Content-Type", "text/event-stream")
		return response, nil
	})

	var deltas []string
	resp, err := adapter.Chat(context.Background(), testProvider("http://test.local"), "", ChatRequest{
		Model:  "m1",
		Stream: true,
		OnDelta: func(d string) {
			deltas = append(deltas, d)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "你好" || resp.Model != "m1" {
		t.Errorf("stream resp = %+v", resp)
	}
	if strings.Join(deltas, "") != "你好" {
		t.Errorf("deltas = %v", deltas)
	}
}

// blockingBody serves data, then waits for the request context and fails, so
// a canceled stream behaves like a stalled connection.
type blockingBody struct {
	ctx  context.Context
	data []byte
	off  int
}

func (b *blockingBody) Read(p []byte) (int, error) {
	if b.off < len(b.data) {
		n := copy(p, b.data[b.off:])
		b.off += n
		return n, nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *blockingBody) Close() error { return nil }

func TestOpenAIChatStreamCancel(t *testing.T) {
	adapter := NewOpenAIAdapter()
	adapter.Client = testClient(func(r *http.Request) (*http.Response, error) {
		body := &blockingBody{
			ctx:  r.Context(),
			data: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n"),
		}
		response := jsonResponse(http.StatusOK, "")
		response.Header.Set("Content-Type", "text/event-stream")
		response.Body = body
		return response, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := adapter.Chat(ctx, testProvider("http://test.local"), "", ChatRequest{
		Model:   "m1",
		Stream:  true,
		OnDelta: func(string) { cancel() },
	})
	if err == nil {
		t.Fatal("canceled stream returned no error")
	}
	if got := KindOf(err); got != KindInternal {
		t.Errorf("kind = %s, want %s", got, KindInternal)
	}
}

func TestOpenAIEmbed(t *testing.T) {
	adapter := NewOpenAIAdapter()
	adapter.Client = testClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		return jsonResponse(http.StatusOK,
			`{"model":"e1","data":[{"embedding":[1,2]},{"embedding":[3,4]}],"usage":{"prompt_tokens":4}}`), nil
	})

	resp, err := adapter.Embed(context.Background(), testProvider("http://test.local"), "", EmbedRequest{
		Model: "e1", Input: []string{"a", "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Vectors) != 2 || resp.Vectors[1][0] != 3 {
		t.Errorf("vectors = %+v", resp.Vectors)
	}
	if !resp.HasUsage || resp.InputTokens != 4 {
		t.Errorf("usage = %+v", resp)
	}
}

func TestOpenAIUnparseableResponse(t *testing.T) {
	adapter := NewOpenAIAdapter()
	adapter.Client = testClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `not json`), nil
	})
	_, _, err := adapter.Test(context.Background(), testProvider("http://test.local"), "")
	if got := KindOf(err); got != KindInternal {
		t.Errorf("kind = %s, want %s", got, KindInternal)
	}
}
