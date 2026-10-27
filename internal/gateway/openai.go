package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Default timeouts of the OpenAI compatible adapter.
const (
	defaultTestTimeout   = 15 * time.Second
	defaultChatTimeout   = 60 * time.Second
	defaultStreamTimeout = 120 * time.Second
	defaultStreamIdle    = 30 * time.Second
	maxResponseBytes     = 8 << 20
)

// OpenAIAdapter speaks the OpenAI compatible HTTP protocol: /models for the
// connection test, /chat/completions and /embeddings for calls.
type OpenAIAdapter struct {
	Client        *http.Client
	TestTimeout   time.Duration
	ChatTimeout   time.Duration
	StreamTimeout time.Duration
	StreamIdle    time.Duration
}

// NewOpenAIAdapter returns the adapter with the default client and timeouts.
func NewOpenAIAdapter() *OpenAIAdapter {
	return &OpenAIAdapter{
		Client: &http.Client{Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        16,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     60 * time.Second,
		}},
		TestTimeout:   defaultTestTimeout,
		ChatTimeout:   defaultChatTimeout,
		StreamTimeout: defaultStreamTimeout,
		StreamIdle:    defaultStreamIdle,
	}
}

// Type returns the provider type this adapter serves.
func (a *OpenAIAdapter) Type() string { return ProviderOpenAI }

// Test calls /models and returns the model IDs the service offers.
func (a *OpenAIAdapter) Test(ctx context.Context, p Provider, key string) (TestResult, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.TestTimeout)
	defer cancel()
	base, err := NormalizeBaseURL(p.BaseURL)
	if err != nil {
		return TestResult{}, nil, internalError("供应商地址无效", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return TestResult{}, nil, internalError("无法创建测试请求", err)
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return TestResult{}, nil, classifyTransport(ctx, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return TestResult{}, nil, classifyTransport(ctx, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TestResult{}, nil, classifyStatus(resp.StatusCode, body)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return TestResult{}, nil, internalError("连接成功但响应无法解析", err)
	}
	models := make([]string, 0, len(payload.Data))
	seen := make(map[string]bool, len(payload.Data))
	for _, m := range payload.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
		if len(models) == MaxModels {
			break
		}
	}
	message := fmt.Sprintf("%d 个模型可用", len(models))
	if len(models) == 0 {
		message = "连接成功，但没有返回模型"
	}
	result := TestResult{OK: true, Kind: TestKindModels, Message: message, At: time.Now().Unix()}
	return result, models, nil
}

type chatBody struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

// Chat calls /chat/completions, reading a stream when req.Stream is set.
func (a *OpenAIAdapter) Chat(ctx context.Context, p Provider, key string, req ChatRequest) (ChatResponse, error) {
	start := time.Now()
	base, err := NormalizeBaseURL(p.BaseURL)
	if err != nil {
		return ChatResponse{}, internalError("供应商地址无效", err)
	}
	timeout := a.ChatTimeout
	if req.Stream {
		timeout = a.StreamTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := json.Marshal(chatBody{
		Model:       req.Model,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      req.Stream,
	})
	if err != nil {
		return ChatResponse{}, internalError("无法编码请求", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, internalError("无法创建请求", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := a.Client.Do(httpReq)
	if err != nil {
		return ChatResponse{}, classifyTransport(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return ChatResponse{}, classifyStatus(resp.StatusCode, data)
	}
	if req.Stream {
		return a.readStream(ctx, resp, req, p, start)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return ChatResponse{}, classifyTransport(ctx, err)
	}
	var payload struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ChatResponse{}, internalError("响应无法解析", err)
	}
	if len(payload.Choices) == 0 {
		return ChatResponse{}, internalError("响应中没有回答", nil)
	}
	out := ChatResponse{
		Content:    payload.Choices[0].Message.Content,
		Model:      payload.Model,
		Duration:   time.Since(start),
		ProviderID: p.ID,
	}
	if out.Model == "" {
		out.Model = req.Model
	}
	if payload.Usage != nil {
		out.HasUsage = true
		out.InputTokens = payload.Usage.PromptTokens
		out.OutputTokens = payload.Usage.CompletionTokens
	}
	return out, nil
}

// readStream parses text/event-stream lines and returns the joined content.
func (a *OpenAIAdapter) readStream(ctx context.Context, resp *http.Response, req ChatRequest, p Provider, start time.Time) (ChatResponse, error) {
	type lineResult struct {
		text string
		err  error
	}
	lines := make(chan lineResult, 1)
	readCtx, stopRead := context.WithCancel(ctx)
	defer stopRead()
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			select {
			case lines <- lineResult{text: scanner.Text()}:
			case <-readCtx.Done():
				return
			}
		}
		select {
		case lines <- lineResult{err: scanner.Err()}:
		case <-readCtx.Done():
			return
		}
		close(lines)
	}()

	var content strings.Builder
	model := ""
	idle := time.NewTimer(a.StreamIdle)
	defer idle.Stop()
	finish := func() (ChatResponse, error) {
		if model == "" {
			model = req.Model
		}
		return ChatResponse{
			Content:    content.String(),
			Model:      model,
			Duration:   time.Since(start),
			ProviderID: p.ID,
		}, nil
	}
	for {
		select {
		case <-ctx.Done():
			return ChatResponse{}, classifyTransport(ctx, ctx.Err())
		case <-idle.C:
			return ChatResponse{}, &Error{Kind: KindTimeout, Message: "流式响应超时"}
		case result, ok := <-lines:
			if !ok {
				return finish()
			}
			if result.err != nil {
				return ChatResponse{}, classifyTransport(ctx, result.err)
			}
			idle.Reset(a.StreamIdle)
			done, err := parseStreamLine(result.text, req.OnDelta, &content, &model)
			if err != nil {
				return ChatResponse{}, err
			}
			if done {
				return finish()
			}
		}
	}
}

func parseStreamLine(line string, onDelta func(string), content *strings.Builder, model *string) (bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
		return false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "[DONE]" {
		return true, nil
	}
	var chunk struct {
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		// Compatible services sometimes send keep-alive data we do not know.
		return false, nil
	}
	if chunk.Error != nil {
		if isModerationBody([]byte(payload)) {
			return false, &Error{Kind: KindModeration, Message: "请求被内容审核拒绝"}
		}
		return false, internalError("流式响应返回错误", nil)
	}
	if chunk.Model != "" {
		*model = chunk.Model
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content == "" {
			continue
		}
		content.WriteString(choice.Delta.Content)
		if onDelta != nil {
			onDelta(choice.Delta.Content)
		}
	}
	return false, nil
}

// Embed calls /embeddings for one or more texts.
func (a *OpenAIAdapter) Embed(ctx context.Context, p Provider, key string, req EmbedRequest) (EmbedResponse, error) {
	start := time.Now()
	base, err := NormalizeBaseURL(p.BaseURL)
	if err != nil {
		return EmbedResponse{}, internalError("供应商地址无效", err)
	}
	ctx, cancel := context.WithTimeout(ctx, a.ChatTimeout)
	defer cancel()
	body, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{Model: req.Model, Input: req.Input})
	if err != nil {
		return EmbedResponse{}, internalError("无法编码请求", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return EmbedResponse{}, internalError("无法创建请求", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := a.Client.Do(httpReq)
	if err != nil {
		return EmbedResponse{}, classifyTransport(ctx, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return EmbedResponse{}, classifyTransport(ctx, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return EmbedResponse{}, classifyStatus(resp.StatusCode, data)
	}
	var payload struct {
		Model string `json:"model"`
		Data  []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage *struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return EmbedResponse{}, internalError("响应无法解析", err)
	}
	if len(payload.Data) == 0 {
		return EmbedResponse{}, internalError("响应中没有向量", nil)
	}
	out := EmbedResponse{
		Model:      payload.Model,
		Duration:   time.Since(start),
		ProviderID: p.ID,
	}
	if out.Model == "" {
		out.Model = req.Model
	}
	for _, d := range payload.Data {
		out.Vectors = append(out.Vectors, d.Embedding)
	}
	if payload.Usage != nil {
		out.HasUsage = true
		out.InputTokens = payload.Usage.PromptTokens
	}
	return out, nil
}
