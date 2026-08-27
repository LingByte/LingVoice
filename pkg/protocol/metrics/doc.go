// Package metrics exposes Prometheus metrics for the LingVoice protocol layer.
//
// The Collector created by NewCollector tracks session lifecycle, media
// throughput (frames and bytes in/out), track counts, signalling events,
// commands and HTTP traffic. Metrics are registered with the default
// Prometheus registry and can be served on the /metrics endpoint via the
// Handler method. The Middleware method can be wrapped around any
// http.Handler to automatically record request counts and durations.
//
// NewCollector is safe to call multiple times; the underlying metrics are only
// registered once thanks to a sync.Once guard, which prevents the duplicate
// registration panics that the Prometheus client would otherwise raise.
package metrics
