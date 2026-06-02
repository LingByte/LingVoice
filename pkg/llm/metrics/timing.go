package metrics

import "time"

// RunTiming holds latency metrics for one model call.
type RunTiming struct {
	// UpstreamLatency is provider HTTP round-trip (non-stream) or until stream is ready.
	UpstreamLatency time.Duration
	// TTFT is time to first token/chunk with model output.
	// Non-stream: equals upstream latency (full response at once).
	TTFT time.Duration
	// TokensPerSecond is filled by handler from RunRecord; not set on CallbackTiming for non-stream.
	TokensPerSecond float64
}

// TokensPerSecond computes throughput for a finished run.
// Non-stream: total_tokens / duration.
// Stream: completion_tokens / (duration - ttft).
func TokensPerSecond(rec RunRecord) float64 {
	if rec.Usage == nil || rec.DurationMs <= 0 {
		return 0
	}
	if !rec.Stream {
		return float64(rec.Usage.TotalTokens) / (rec.DurationMs / 1000)
	}
	genMs := rec.DurationMs - rec.TTFTMs
	if genMs <= 0 {
		genMs = rec.DurationMs
	}
	n := rec.Usage.CompletionTokens
	if n <= 0 {
		n = rec.Usage.TotalTokens
	}
	if n <= 0 {
		return 0
	}
	return float64(n) / (genMs / 1000)
}

// FinalizeStream sets TokensPerSecond from completion tokens after first token.
func (t *RunTiming) FinalizeStream(completionTokens int, firstTokenAt, endedAt time.Time) {
	if t == nil || completionTokens <= 0 || firstTokenAt.IsZero() {
		return
	}
	window := endedAt.Sub(firstTokenAt)
	if window <= 0 {
		return
	}
	t.TokensPerSecond = float64(completionTokens) / window.Seconds()
}
