package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/LingByte/LingVoice/pkg/llm/internal/httputil"
)

// ErrorType classifies provider / transport failures for dashboards and retry policy.
type ErrorType string

const (
	ErrorTypeNone             ErrorType = ""
	ErrorTypeTimeout          ErrorType = "timeout"
	ErrorTypeRateLimit        ErrorType = "rate_limit"
	ErrorTypePermission       ErrorType = "permission"
	ErrorTypeModelUnavailable ErrorType = "model_unavailable"
	ErrorTypeContentFilter    ErrorType = "content_filter"
	ErrorTypeNetwork          ErrorType = "network"
	ErrorTypeUnknown          ErrorType = "unknown"
)

// ClassifyError maps an error to ErrorType and a stable error code string.
func ClassifyError(err error) (ErrorType, string) {
	if err == nil {
		return ErrorTypeNone, ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTypeTimeout, "context_deadline"
	}
	if errors.Is(err, context.Canceled) {
		return ErrorTypeTimeout, "context_canceled"
	}
	var he *httputil.HTTPError
	if errors.As(err, &he) {
		code := he.Code
		if code == "" {
			code = fmt.Sprintf("http_%d", he.StatusCode)
		}
		return classifyHTTP(he.StatusCode, he.Body, code), code
	}
	var ne net.Error
	if errors.As(err, &ne) {
		if ne.Timeout() {
			return ErrorTypeTimeout, "network_timeout"
		}
		return ErrorTypeNetwork, "network_error"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return ErrorTypeTimeout, "timeout"
	case strings.Contains(msg, "rate limit"), strings.Contains(msg, "429"), strings.Contains(msg, "too many"):
		return ErrorTypeRateLimit, "rate_limit"
	case strings.Contains(msg, "unauthorized"), strings.Contains(msg, "401"), strings.Contains(msg, "403"), strings.Contains(msg, "permission"), strings.Contains(msg, "api key"):
		return ErrorTypePermission, "permission_denied"
	case strings.Contains(msg, "model"), strings.Contains(msg, "not found"), strings.Contains(msg, "unavailable"):
		return ErrorTypeModelUnavailable, "model_unavailable"
	case strings.Contains(msg, "content"), strings.Contains(msg, "filter"), strings.Contains(msg, "policy"), strings.Contains(msg, "moderation"):
		return ErrorTypeContentFilter, "content_filter"
	case strings.Contains(msg, "connection"), strings.Contains(msg, "dial"), strings.Contains(msg, "eof"), strings.Contains(msg, "reset"):
		return ErrorTypeNetwork, "network_error"
	default:
		return ErrorTypeUnknown, "unknown"
	}
}

func classifyHTTP(status int, body, code string) ErrorType {
	b := strings.ToLower(body)
	c := strings.ToLower(code)
	switch status {
	case 408, 504:
		return ErrorTypeTimeout
	case 429:
		return ErrorTypeRateLimit
	case 401, 403:
		return ErrorTypePermission
	case 404:
		return ErrorTypeModelUnavailable
	case 503, 502:
		return ErrorTypeModelUnavailable
	case 500:
		if strings.Contains(b, "model") || strings.Contains(b, "overload") || strings.Contains(b, "capacity") {
			return ErrorTypeModelUnavailable
		}
	}
	if strings.Contains(c, "rate") || strings.Contains(b, "rate limit") || strings.Contains(b, "too many requests") {
		return ErrorTypeRateLimit
	}
	if strings.Contains(c, "content_filter") || strings.Contains(c, "content_policy") ||
		strings.Contains(b, "content_filter") || strings.Contains(b, "content filter") ||
		strings.Contains(b, "moderation") {
		return ErrorTypeContentFilter
	}
	if strings.Contains(c, "permission") || strings.Contains(c, "authentication") {
		return ErrorTypePermission
	}
	if strings.Contains(c, "model") || strings.Contains(b, "model_not_found") {
		return ErrorTypeModelUnavailable
	}
	return ErrorTypeUnknown
}
