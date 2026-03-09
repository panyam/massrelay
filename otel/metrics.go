package otel

import (
	"log"

	"go.opentelemetry.io/otel/metric"
)

// Metrics holds all relay metric instruments.
type Metrics struct {
	// Gauges (current state)
	RoomsActive metric.Int64UpDownCounter
	PeersActive metric.Int64UpDownCounter

	// Counters (cumulative)
	ConnectionsTotal metric.Int64Counter
	MessagesTotal    metric.Int64Counter
	JoinsTotal       metric.Int64Counter
	LeavesTotal      metric.Int64Counter
	RateLimited      metric.Int64Counter
	DroppedMessages  metric.Int64Counter

	// Histograms
	MessageSize metric.Int64Histogram
}

// NewMetrics creates all metric instruments using the given provider.
// Pass nil to use the global provider (set by Setup or by the host app).
// Safe to call even when OTEL is no-op — instruments will simply not record.
func NewMetrics(provider metric.MeterProvider) *Metrics {
	m := MeterFrom(provider)
	var metrics Metrics
	var err error

	metrics.RoomsActive, err = m.Int64UpDownCounter("relay.rooms.active",
		metric.WithDescription("Number of active rooms"),
		metric.WithUnit("{room}"))
	logErr("relay.rooms.active", err)

	metrics.PeersActive, err = m.Int64UpDownCounter("relay.peers.active",
		metric.WithDescription("Number of connected peers"),
		metric.WithUnit("{peer}"))
	logErr("relay.peers.active", err)

	metrics.ConnectionsTotal, err = m.Int64Counter("relay.connections.total",
		metric.WithDescription("Total WebSocket connections"),
		metric.WithUnit("{connection}"))
	logErr("relay.connections.total", err)

	metrics.MessagesTotal, err = m.Int64Counter("relay.messages.total",
		metric.WithDescription("Total messages relayed"),
		metric.WithUnit("{message}"))
	logErr("relay.messages.total", err)

	metrics.JoinsTotal, err = m.Int64Counter("relay.joins.total",
		metric.WithDescription("Total room joins"),
		metric.WithUnit("{join}"))
	logErr("relay.joins.total", err)

	metrics.LeavesTotal, err = m.Int64Counter("relay.leaves.total",
		metric.WithDescription("Total room leaves"),
		metric.WithUnit("{leave}"))
	logErr("relay.leaves.total", err)

	metrics.RateLimited, err = m.Int64Counter("relay.rate_limited.total",
		metric.WithDescription("Total rate-limited requests"),
		metric.WithUnit("{request}"))
	logErr("relay.rate_limited.total", err)

	metrics.DroppedMessages, err = m.Int64Counter("relay.messages.dropped",
		metric.WithDescription("Messages dropped due to full send channel"),
		metric.WithUnit("{message}"))
	logErr("relay.messages.dropped", err)

	metrics.MessageSize, err = m.Int64Histogram("relay.message.size",
		metric.WithDescription("Message payload size in bytes"),
		metric.WithUnit("By"))
	logErr("relay.message.size", err)

	return &metrics
}

func logErr(name string, err error) {
	if err != nil {
		log.Printf("[OTEL] Failed to create metric %s: %v", name, err)
	}
}
