package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrorKind classifies a gateway failure. The settings page shows its label,
// retry and fallback follow its rules.
type ErrorKind string

// Error kinds.
const (
	KindAuth       ErrorKind = "auth"
	KindQuota      ErrorKind = "quota"
	KindModeration ErrorKind = "moderation"
	KindModel      ErrorKind = "model"
	KindNetwork    ErrorKind = "network"
	KindTimeout    ErrorKind = "timeout"
	KindServer     ErrorKind = "server"
	KindInternal   ErrorKind = "internal"
)

// Error is a classified gateway failure. Message is user-facing and never
// contains a key, a request body or a full URL.
type Error struct {
	Kind    ErrorKind
	Status  int
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the kind of err, internal when it is not classified.
func KindOf(err error) ErrorKind {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Kind
	}
	return KindInternal
}

// Retryable reports whether a failed call may be repeated: only network,
// timeout and server failures retry.
func Retryable(err error) bool {
	switch KindOf(err) {
	case KindNetwork, KindTimeout, KindServer:
		return true
	default:
		return false
	}
}

// FallbackAllowed reports whether a route may try its next target. Content
// moderation stops the request, and so do local failures.
func FallbackAllowed(err error) bool {
	switch KindOf(err) {
	case KindModeration, KindInternal:
		return false
	default:
		return true
	}
}

// KindText returns the Chinese label of a kind.
func KindText(kind ErrorKind) string {
	switch kind {
	case KindAuth:
		return "鉴权"
	case KindQuota:
		return "额度"
	case KindModeration:
		return "内容审核"
	case KindModel:
		return "模型"
	case KindNetwork:
		return "网络"
	case KindTimeout:
		return "超时"
	case KindServer:
		return "服务端"
	default:
		return "内部"
	}
}

// classifyStatus turns an HTTP failure into a classified error. The body is
// only inspected for the content moderation code and is never included in the
// message.
func classifyStatus(status int, body []byte) *Error {
	if isModerationBody(body) {
		return &Error{Kind: KindModeration, Status: status,
			Message: fmt.Sprintf("请求被内容审核拒绝（HTTP %d）", status)}
	}
	switch {
	case status == 401 || status == 403:
		return &Error{Kind: KindAuth, Status: status,
			Message: fmt.Sprintf("鉴权失败（HTTP %d），请检查 API 密钥", status)}
	case status == 429:
		return &Error{Kind: KindQuota, Status: status,
			Message: fmt.Sprintf("请求被限流或额度不足（HTTP %d）", status)}
	case status == 400 || status == 404:
		return &Error{Kind: KindModel, Status: status,
			Message: fmt.Sprintf("模型或参数无效（HTTP %d）", status)}
	case status == 408 || status == 504:
		return &Error{Kind: KindTimeout, Status: status,
			Message: fmt.Sprintf("请求超时（HTTP %d）", status)}
	case status >= 500:
		return &Error{Kind: KindServer, Status: status,
			Message: fmt.Sprintf("服务端错误（HTTP %d）", status)}
	default:
		return &Error{Kind: KindInternal, Status: status,
			Message: fmt.Sprintf("请求失败（HTTP %d）", status)}
	}
}

func isModerationBody(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, marker := range []string{"content_filter", "content_policy", "moderation"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// classifyTransport turns a client-side failure into a classified error,
// falling back to network for anything that is not a timeout or cancellation.
func classifyTransport(ctx context.Context, err error) *Error {
	ctxErr := ctx.Err()
	if errors.Is(ctxErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: KindTimeout, Message: "请求超时", Err: err}
	}
	if errors.Is(ctxErr, context.Canceled) || errors.Is(err, context.Canceled) {
		return &Error{Kind: KindInternal, Message: "请求已取消", Err: err}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &Error{Kind: KindTimeout, Message: "连接超时", Err: err}
	}
	return &Error{Kind: KindNetwork, Message: "网络连接失败", Err: err}
}

// internalError wraps a local failure that is not network related.
func internalError(message string, err error) *Error {
	return &Error{Kind: KindInternal, Message: message, Err: err}
}
