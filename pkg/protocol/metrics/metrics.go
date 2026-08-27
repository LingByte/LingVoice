// Package metrics provides a Prometheus metrics collector for the LingVoice
// protocol layer. It exposes counters, gauges and histograms that describe
// session lifecycle, media throughput, signalling events, commands and HTTP
// traffic so that operators can observe the system through a standard
// /metrics endpoint.
package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector groups every Prometheus metric that the protocol layer publishes.
// A single Collector should be created with NewCollector and shared across the
// application; the metrics are registered against the default Prometheus
// registry so that they are automatically served by Handler.
type Collector struct {
	sessionsTotal       *prometheus.CounterVec
	sessionsActive      *prometheus.GaugeVec
	mediaFramesIn       *prometheus.CounterVec
	mediaFramesOut      *prometheus.CounterVec
	mediaBytesIn        *prometheus.CounterVec
	mediaBytesOut       *prometheus.CounterVec
	trackCount          *prometheus.GaugeVec
	eventTotal          *prometheus.CounterVec
	commandTotal        *prometheus.CounterVec
	httpRequests        *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
}

// registered holds the single Collector instance that has been registered with
// the default Prometheus registry. It is guarded by registerOnce so that
// repeated calls to NewCollector do not trigger duplicate-registration panics.
var (
	registered  *Collector
	registerOnce sync.Once
)

// NewCollector creates a new Collector and registers all of its metrics with
// prometheus.DefaultRegisterer. It is safe to call multiple times: the first
// successful call installs the metrics, subsequent calls return the already
// registered Collector without attempting to re-register.
func NewCollector() *Collector {
	registerOnce.Do(func() {
		c := &Collector{
			sessionsTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "lingvoice_sessions_total",
					Help: "Total number of sessions created, partitioned by protocol and status.",
				},
				[]string{"protocol", "status"},
			),
			sessionsActive: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "lingvoice_sessions_active",
					Help: "Number of currently active sessions, partitioned by protocol.",
				},
				[]string{"protocol"},
			),
			mediaFramesIn: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "lingvoice_media_frames_in_total",
					Help: "Total number of inbound media frames, partitioned by protocol, codec and frame type.",
				},
				[]string{"protocol", "codec", "type"},
			),
			mediaFramesOut: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "lingvoice_media_frames_out_total",
					Help: "Total number of outbound media frames, partitioned by protocol, codec and frame type.",
				},
				[]string{"protocol", "codec", "type"},
			),
			mediaBytesIn: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "lingvoice_media_bytes_in_total",
					Help: "Total number of inbound media bytes, partitioned by protocol, codec and frame type.",
				},
				[]string{"protocol", "codec", "type"},
			),
			mediaBytesOut: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "lingvoice_media_bytes_out_total",
					Help: "Total number of outbound media bytes, partitioned by protocol, codec and frame type.",
				},
				[]string{"protocol", "codec", "type"},
			),
			trackCount: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "lingvoice_track_count",
					Help: "Number of media tracks, partitioned by protocol and kind.",
				},
				[]string{"protocol", "kind"},
			),
			eventTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "lingvoice_event_total",
					Help: "Total number of signalling events, partitioned by protocol and event type.",
				},
				[]string{"protocol", "event_type"},
			),
			commandTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "lingvoice_command_total",
					Help: "Total number of commands processed, partitioned by protocol and command type.",
				},
				[]string{"protocol", "command_type"},
			),
			httpRequests: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "lingvoice_http_requests_total",
					Help: "Total number of HTTP requests, partitioned by method, path and status.",
				},
				[]string{"method", "path", "status"},
			),
			httpRequestDuration: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "lingvoice_http_request_duration_seconds",
					Help:    "HTTP request latency in seconds, partitioned by method and path.",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"method", "path"},
			),
		}

		registered = c
		prometheus.MustRegister(
			c.sessionsTotal,
			c.sessionsActive,
			c.mediaFramesIn,
			c.mediaFramesOut,
			c.mediaBytesIn,
			c.mediaBytesOut,
			c.trackCount,
			c.eventTotal,
			c.commandTotal,
			c.httpRequests,
			c.httpRequestDuration,
		)
	})

	return registered
}

// IncSession increments the session counter for the given protocol and status.
// status is typically "created", "closed" or "failed".
func (c *Collector) IncSession(protocol string, status string) {
	c.sessionsTotal.WithLabelValues(protocol, status).Inc()
}

// SetActiveSessions sets the number of currently active sessions for the given
// protocol.
func (c *Collector) SetActiveSessions(protocol string, count int) {
	c.sessionsActive.WithLabelValues(protocol).Set(float64(count))
}

// IncMediaFrame records a media frame. direction must be either "in" or "out";
// bytes is the payload size of the frame.
func (c *Collector) IncMediaFrame(protocol string, codec string, frameType string, bytes int, direction string) {
	switch direction {
	case "in":
		c.mediaFramesIn.WithLabelValues(protocol, codec, frameType).Inc()
		c.mediaBytesIn.WithLabelValues(protocol, codec, frameType).Add(float64(bytes))
	case "out":
		c.mediaFramesOut.WithLabelValues(protocol, codec, frameType).Inc()
		c.mediaBytesOut.WithLabelValues(protocol, codec, frameType).Add(float64(bytes))
	default:
		// Unknown direction is ignored to avoid creating spurious label series.
	}
}

// IncEvent increments the event counter for the given protocol and event type.
func (c *Collector) IncEvent(protocol string, eventType string) {
	c.eventTotal.WithLabelValues(protocol, eventType).Inc()
}

// IncCommand increments the command counter for the given protocol and command
// type.
func (c *Collector) IncCommand(protocol string, cmdType string) {
	c.commandTotal.WithLabelValues(protocol, cmdType).Inc()
}

// Handler returns an http.Handler that exposes the registered Prometheus
// metrics on the /metrics endpoint.
func (c *Collector) Handler() http.Handler {
	return promhttp.Handler()
}

// statusWriter wraps an http.ResponseWriter so that the status code can be
// captured after the handler has written its response.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Middleware returns an http.Handler middleware that records the request count
// and duration for every request that passes through it.
func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(sw, r)

		elapsed := time.Since(start).Seconds()
		status := strconv.Itoa(sw.status)
		c.httpRequests.WithLabelValues(r.Method, r.URL.Path, status).Inc()
		c.httpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(elapsed)
	})
}
