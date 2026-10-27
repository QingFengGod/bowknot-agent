package gateway

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestClassifyStatus(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   ErrorKind
	}{
		{401, `{"error":"bad key"}`, KindAuth},
		{403, `{"error":"forbidden"}`, KindAuth},
		{429, `{"error":"rate limited"}`, KindQuota},
		{400, `{"error":"invalid model"}`, KindModel},
		{404, `{"error":"not found"}`, KindModel},
		{400, `{"error":{"code":"content_filter"}}`, KindModeration},
		{408, `{}`, KindTimeout},
		{504, `{}`, KindTimeout},
		{500, `{}`, KindServer},
		{503, `{}`, KindServer},
		{418, `{}`, KindInternal},
	}
	for _, tc := range tests {
		if got := classifyStatus(tc.status, []byte(tc.body)).Kind; got != tc.want {
			t.Errorf("classifyStatus(%d, %q) = %s, want %s", tc.status, tc.body, got, tc.want)
		}
	}
}

func TestRetryAndFallbackRules(t *testing.T) {
	tests := []struct {
		kind     ErrorKind
		retry    bool
		fallback bool
	}{
		{KindAuth, false, true},
		{KindQuota, false, true},
		{KindModeration, false, false},
		{KindModel, false, true},
		{KindNetwork, true, true},
		{KindTimeout, true, true},
		{KindServer, true, true},
		{KindInternal, false, false},
	}
	for _, tc := range tests {
		err := &Error{Kind: tc.kind}
		if got := Retryable(err); got != tc.retry {
			t.Errorf("Retryable(%s) = %v, want %v", tc.kind, got, tc.retry)
		}
		if got := FallbackAllowed(err); got != tc.fallback {
			t.Errorf("FallbackAllowed(%s) = %v, want %v", tc.kind, got, tc.fallback)
		}
	}
}

func TestClassifyTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := classifyTransport(ctx, context.Canceled).Kind; got != KindInternal {
		t.Errorf("canceled = %s, want %s", got, KindInternal)
	}

	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 0)
	defer timeoutCancel()
	<-timeoutCtx.Done()
	if got := classifyTransport(timeoutCtx, timeoutCtx.Err()).Kind; got != KindTimeout {
		t.Errorf("deadline = %s, want %s", got, KindTimeout)
	}

	var ne net.Error = &net.DNSError{IsTimeout: true}
	if got := classifyTransport(context.Background(), ne).Kind; got != KindTimeout {
		t.Errorf("net timeout = %s, want %s", got, KindTimeout)
	}
	if got := classifyTransport(context.Background(), errors.New("boom")).Kind; got != KindNetwork {
		t.Errorf("plain error = %s, want %s", got, KindNetwork)
	}
}

func TestKindOfWrapped(t *testing.T) {
	wrapped := errors.Join(errors.New("outer"), &Error{Kind: KindQuota})
	if got := KindOf(wrapped); got != KindQuota {
		t.Errorf("KindOf wrapped = %s, want %s", got, KindQuota)
	}
	if got := KindOf(errors.New("plain")); got != KindInternal {
		t.Errorf("KindOf plain = %s, want %s", got, KindInternal)
	}
}
