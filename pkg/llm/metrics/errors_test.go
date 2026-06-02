package metrics_test

import (
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/internal/httputil"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestClassifyError_HTTP(t *testing.T) {
	err := httputil.NewHTTPError(429, `{"error":{"code":"rate_limit_exceeded","message":"too many"}}`)
	typ, code := metrics.ClassifyError(err)
	if typ != metrics.ErrorTypeRateLimit {
		t.Fatalf("type=%q", typ)
	}
	if code != "rate_limit_exceeded" {
		t.Fatalf("code=%q", code)
	}
}

func TestClassifyError_ContentFilter(t *testing.T) {
	err := httputil.NewHTTPError(400, `{"error":{"code":"content_filter","message":"blocked"}}`)
	typ, _ := metrics.ClassifyError(err)
	if typ != metrics.ErrorTypeContentFilter {
		t.Fatalf("type=%q", typ)
	}
}

func TestTokensPerSecond_NonStream(t *testing.T) {
	rec := metrics.RunRecord{
		Stream:     false,
		DurationMs: 1724,
		Usage:      &schema.TokenUsage{TotalTokens: 83, CompletionTokens: 55, PromptTokens: 28},
	}
	got := metrics.TokensPerSecond(rec)
	want := 83.0 / 1.724
	if got < want*0.99 || got > want*1.01 {
		t.Fatalf("tokens/s=%f want~%f", got, want)
	}
}

func TestTokensPerSecond_Stream(t *testing.T) {
	rec := metrics.RunRecord{
		Stream:     true,
		DurationMs: 1000,
		TTFTMs:     200,
		Usage:      &schema.TokenUsage{CompletionTokens: 80, TotalTokens: 100},
	}
	got := metrics.TokensPerSecond(rec)
	want := 80.0 / 0.8
	if got < want*0.99 || got > want*1.01 {
		t.Fatalf("tokens/s=%f want~%f", got, want)
	}
}
