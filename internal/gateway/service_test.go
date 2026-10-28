package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBase is the host the fake transport answers; nothing listens there.
const fakeBase = "http://test.local"

func newTestService(t *testing.T, handler roundTripFunc) (*Service, string) {
	t.Helper()
	svc, err := NewService(Options{Dir: t.TempDir(), Client: testClient(handler)})
	if err != nil {
		t.Fatal(err)
	}
	return svc, fakeBase
}

func notFoundHandler() roundTripFunc {
	return statusHandler(http.StatusNotFound, `{"error":"not found"}`)
}

func statusHandler(status int, body string) roundTripFunc {
	return func(*http.Request) (*http.Response, error) {
		return jsonResponse(status, body), nil
	}
}

func addProvider(t *testing.T, svc *Service, baseURL, name, path string, models ...string) Provider {
	t.Helper()
	p, err := svc.SaveProvider(Provider{
		Name:    name,
		Type:    ProviderOpenAI,
		BaseURL: baseURL + path,
		Enabled: true,
		Models:  modelsFromIDs(models),
	}, "sk-"+name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServiceSaveProviderKeepsConfigSecretless(t *testing.T) {
	svc, url := newTestService(t, notFoundHandler())
	p := addProvider(t, svc, url, "Primary", "/v1", "m1", "m2")

	if !strings.HasPrefix(p.ID, "p-") || len(p.ID) != 10 {
		t.Errorf("id = %q, want p- and 8 hex digits", p.ID)
	}
	entries := svc.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].KeyMasked != "••••mary" {
		t.Errorf("mask = %q, want ••••mary", entries[0].KeyMasked)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(svc.config), ProvidersFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-Primary") {
		t.Error("providers.json holds the plaintext key")
	}
	if svc.secrets.Secret(p.ID) != "sk-Primary" {
		t.Error("stored key missing")
	}

	// An empty key keeps the stored one.
	if _, err := svc.SaveProvider(Provider{ID: p.ID, Name: "Primary", Type: ProviderOpenAI,
		BaseURL: p.BaseURL, Enabled: true, Models: p.Models}, ""); err != nil {
		t.Fatal(err)
	}
	if svc.secrets.Secret(p.ID) != "sk-Primary" {
		t.Error("empty key did not keep the stored key")
	}

	// The first saved provider seeds the chat and embedding routes.
	chat := svc.RouteTargets(CapabilityChat)
	if len(chat) != 1 || chat[0].Model != "m1" {
		t.Errorf("chat routes = %+v, want m1", chat)
	}
	if _, err := svc.SaveProvider(Provider{Name: " ", Type: ProviderOpenAI,
		BaseURL: url + "/v1"}, ""); err == nil {
		t.Error("invalid provider saved without an error")
	}
}

func TestServiceSnapshotStates(t *testing.T) {
	svc, url := newTestService(t, notFoundHandler())
	if got := svc.Snapshot().State; got != StateNotConfigured {
		t.Fatalf("empty state = %v, want not configured", got)
	}
	p := addProvider(t, svc, url, "Primary", "/v1", "m1")
	snap := svc.Snapshot()
	if snap.State != StateOnline || snap.Model != "m1" {
		t.Errorf("snapshot = %+v, want online with m1 before the first test", snap)
	}
	if snap.Reason == "" {
		t.Error("untested snapshot has no hint")
	}
	if err := svc.SetProviderEnabled(p.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := svc.Snapshot().State; got != StateNotConfigured {
		t.Errorf("disabled state = %v, want not configured", got)
	}
}

func TestServiceTestProviderFillsModelsAndRoutes(t *testing.T) {
	handler := func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/models" {
			return notFoundHandler()(r)
		}
		return jsonResponse(http.StatusOK, `{"data":[{"id":"m1"},{"id":"m2"}]}`), nil
	}
	svc, url := newTestService(t, handler)
	p := addProvider(t, svc, url, "Primary", "/v1")

	result, err := svc.TestProvider(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	entries := svc.Entries()
	if got := modelIDs(entries[0].Provider.Models); got != "m1,m2" {
		t.Errorf("models = %q, want m1,m2", got)
	}
	snap := svc.Snapshot()
	if snap.State != StateOnline || snap.Model != "m1" {
		t.Errorf("snapshot = %+v, want online with m1", snap)
	}
	if len(svc.TargetOptions()) != 2 {
		t.Errorf("target options = %+v", svc.TargetOptions())
	}
}

func TestServiceTestProviderFailureOffline(t *testing.T) {
	svc, url := newTestService(t, statusHandler(http.StatusUnauthorized, `{"error":"bad key"}`))
	p := addProvider(t, svc, url, "Primary", "/v1", "m1")
	result, err := svc.TestProvider(context.Background(), p.ID)
	if err == nil {
		t.Fatal("failed test returned no error")
	}
	if result.OK || KindOf(err) != KindAuth {
		t.Errorf("result = %+v, err kind = %s", result, KindOf(err))
	}
	snap := svc.Snapshot()
	if snap.State != StateOffline || snap.Reason == "" {
		t.Errorf("snapshot = %+v, want offline with a reason", snap)
	}
}

func TestServiceChatFallsBack(t *testing.T) {
	handler := func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/a/v1/chat"):
			return jsonResponse(http.StatusUnauthorized, `{"error":"bad key"}`), nil
		case strings.HasPrefix(r.URL.Path, "/b/v1/chat"):
			return jsonResponse(http.StatusOK,
				`{"model":"m1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`), nil
		default:
			return notFoundHandler()(r)
		}
	}
	svc, url := newTestService(t, handler)
	a := addProvider(t, svc, url, "A", "/a/v1", "m1")
	b := addProvider(t, svc, url, "B", "/b/v1", "m1")
	if err := svc.SaveRoutes(CapabilityChat, []Route{
		{Provider: a.ID, Model: "m1"},
		{Provider: b.ID, Model: "m1"},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := svc.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, "settings")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || resp.ProviderID != b.ID {
		t.Errorf("resp = %+v, want B", resp)
	}
	records, err := svc.usage.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("usage = %d records, want 2", len(records))
	}
	if records[0].Status != UsageOK || records[0].ProviderID != b.ID {
		t.Errorf("newest = %+v, want B ok", records[0])
	}
	if records[1].Status != UsageError || records[1].ErrorCode != KindAuth {
		t.Errorf("oldest = %+v, want A auth error", records[1])
	}
}

func TestServiceChatPinsTarget(t *testing.T) {
	handler := func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK,
			`{"model":"m1","choices":[{"message":{"content":"ok"}}]}`), nil
	}
	svc, url := newTestService(t, handler)
	p := addProvider(t, svc, url, "A", "/v1", "m1", "m2")
	if err := svc.SaveRoutes(CapabilityChat, []Route{
		{Provider: p.ID, Model: "m1"}, {Provider: p.ID, Model: "m2"},
	}); err != nil {
		t.Fatal(err)
	}
	if targets := svc.ChatTargets(); len(targets) != 2 {
		t.Fatalf("chat targets = %d, want 2", len(targets))
	}
	resp, err := svc.Chat(context.Background(), ChatRequest{
		ProviderID: p.ID, Model: "m2",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
	if _, err := svc.Chat(context.Background(), ChatRequest{Model: "missing"}, "agent"); err == nil {
		t.Error("pinning a model outside the routes succeeded")
	}
}

func TestServiceChatRecordsConversationID(t *testing.T) {
	handler := func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK,
			`{"model":"m1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`), nil
	}
	svc, url := newTestService(t, handler)
	p := addProvider(t, svc, url, "A", "/v1", "m1")
	if err := svc.SaveRoutes(CapabilityChat, []Route{{Provider: p.ID, Model: "m1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Chat(context.Background(), ChatRequest{
		ConversationID: "c-1a2b3c4d",
		Messages:       []ChatMessage{{Role: "user", Content: "hi"}},
	}, "agent"); err != nil {
		t.Fatal(err)
	}
	records, err := svc.usage.Recent(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("usage = %d records, want 1", len(records))
	}
	if records[0].Origin != "agent" || records[0].ConversationID != "c-1a2b3c4d" {
		t.Errorf("record = %+v", records[0])
	}
}

func TestServiceChatEmptyRoutes(t *testing.T) {
	svc, _ := newTestService(t, notFoundHandler())
	if _, err := svc.Chat(context.Background(), ChatRequest{}, "settings"); err == nil {
		t.Fatal("chat without a route returned no error")
	}
}

func TestServiceSaveRoutesRejectsUnknownTarget(t *testing.T) {
	svc, url := newTestService(t, notFoundHandler())
	p := addProvider(t, svc, url, "A", "/v1", "m1")
	err := svc.SaveRoutes(CapabilityChat, []Route{{Provider: p.ID, Model: "missing"}})
	if err == nil {
		t.Fatal("unknown model accepted")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("error = %T, want *ValidationError", err)
	}
	if err := svc.SaveRoutes(CapabilityImage, nil); err == nil {
		t.Error("read-only capability accepted a save")
	}
}

func TestServiceDeleteProviderClearsRoutes(t *testing.T) {
	svc, url := newTestService(t, notFoundHandler())
	p := addProvider(t, svc, url, "A", "/v1", "m1")
	if err := svc.DeleteProvider(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(svc.Entries()) != 0 {
		t.Errorf("entries = %+v, want none", svc.Entries())
	}
	if len(svc.RouteTargets(CapabilityChat)) != 0 {
		t.Errorf("routes = %+v, want none", svc.RouteTargets(CapabilityChat))
	}
	if got := svc.Snapshot().State; got != StateNotConfigured {
		t.Errorf("state = %v, want not configured", got)
	}
	if svc.secrets.Secret(p.ID) != "" {
		t.Error("key kept after deleting the provider")
	}
}

func TestServiceChatProviderWritesUsage(t *testing.T) {
	handler := statusHandler(http.StatusOK, `{"choices":[{"message":{"content":"pong"}}]}`)
	svc, url := newTestService(t, handler)
	p := addProvider(t, svc, url, "A", "/v1", "m1")
	resp, err := svc.ChatProvider(context.Background(), p.ID, "m1", ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "ping"}},
	}, "settings")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "pong" {
		t.Errorf("content = %q", resp.Content)
	}
	records, err := svc.usage.Recent(1)
	if err != nil || len(records) != 1 {
		t.Fatalf("usage = %+v, %v", records, err)
	}
	rec := records[0]
	if rec.Origin != "settings" || rec.ProviderName != "A" || rec.Model != "m1" ||
		rec.Capability != CapabilityChat || !rec.UsageEstimated {
		t.Errorf("record = %+v", rec)
	}
	if snap := svc.Snapshot(); snap.State != StateOnline {
		t.Errorf("state = %v, want online after a successful call", snap.State)
	}
	if _, err := svc.ChatProvider(context.Background(), p.ID, "missing", ChatRequest{}, "settings"); err == nil {
		t.Error("unknown model accepted by ChatProvider")
	}
}

func TestServiceConcurrencyLimit(t *testing.T) {
	svc, _ := newTestService(t, notFoundHandler())
	ctx := context.Background()
	for i := 0; i < MaxInFlightPerProvider; i++ {
		release, err := svc.acquire(ctx, "p-00000001")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	shortCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := svc.acquire(shortCtx, "p-00000001"); err == nil {
		t.Error("fifth call acquired the full semaphore")
	}
}
