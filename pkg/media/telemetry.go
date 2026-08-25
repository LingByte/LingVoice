package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"sync/atomic"
	"time"
)

// MediaTelemetry tracks process-wide media metrics using lock-free atomics.
// Inspired by RustPBX telemetry.rs. All counters are atomic for hot-path safety.
type MediaTelemetry struct {
	ActiveSessions  atomic.Int64
	RxPackets       atomic.Int64
	RxLost          atomic.Int64
	RxInternalDrops atomic.Int64
	TxPackets       atomic.Int64
	TxDropped       atomic.Int64
	BytesReceived   atomic.Int64
	BytesSent       atomic.Int64
}

var globalTelemetry = &MediaTelemetry{}

// Telemetry returns the global telemetry instance.
func Telemetry() *MediaTelemetry {
	return globalTelemetry
}

// RecordRx increments receive counters.
func (t *MediaTelemetry) RecordRx(packets, lost, internalDrops int64, bytes int64) {
	t.RxPackets.Add(packets)
	t.RxLost.Add(lost)
	t.RxInternalDrops.Add(internalDrops)
	t.BytesReceived.Add(bytes)
}

// RecordTx increments transmit counters.
func (t *MediaTelemetry) RecordTx(packets, dropped int64, bytes int64) {
	t.TxPackets.Add(packets)
	t.TxDropped.Add(dropped)
	t.BytesSent.Add(bytes)
}

// RegisterSession increments active session count.
func (t *MediaTelemetry) RegisterSession() {
	t.ActiveSessions.Add(1)
}

// UnregisterSession decrements active session count.
func (t *MediaTelemetry) UnregisterSession() {
	t.ActiveSessions.Add(-1)
}

// TelemetrySnapshot is a point-in-time copy of all telemetry counters.
type TelemetrySnapshot struct {
	ActiveSessions  int64
	RxPackets       int64
	RxLost          int64
	RxInternalDrops int64
	TxPackets       int64
	TxDropped       int64
	BytesReceived   int64
	BytesSent       int64
	Timestamp       time.Time
}

// Snapshot returns a consistent point-in-time view of all counters.
func (t *MediaTelemetry) Snapshot() TelemetrySnapshot {
	return TelemetrySnapshot{
		ActiveSessions:  t.ActiveSessions.Load(),
		RxPackets:       t.RxPackets.Load(),
		RxLost:          t.RxLost.Load(),
		RxInternalDrops: t.RxInternalDrops.Load(),
		TxPackets:       t.TxPackets.Load(),
		TxDropped:       t.TxDropped.Load(),
		BytesReceived:   t.BytesReceived.Load(),
		BytesSent:       t.BytesSent.Load(),
		Timestamp:       time.Now(),
	}
}

// Reset zeros all counters. Primarily for testing.
func (t *MediaTelemetry) Reset() {
	t.ActiveSessions.Store(0)
	t.RxPackets.Store(0)
	t.RxLost.Store(0)
	t.RxInternalDrops.Store(0)
	t.TxPackets.Store(0)
	t.TxDropped.Store(0)
	t.BytesReceived.Store(0)
	t.BytesSent.Store(0)
}

// LegStats tracks per-call quality metrics derived from RTCP.
// Inspired by RustPBX leg_stats.rs. All fields are atomic.
type LegStats struct {
	JitterUS        atomic.Int64  // jitter in microseconds
	RTTUS           atomic.Int64  // round-trip time in microseconds
	FractionLost    atomic.Uint32 // fraction lost (0-255, RFC 3550)
	CumulativeLost  atomic.Int64
	PacketsReceived atomic.Int64
	LastPacketTime  atomic.Int64 // unix nano
}

// NewLegStats creates a new per-leg stats tracker.
func NewLegStats() *LegStats {
	return &LegStats{}
}

// RecordPacket updates packet-level stats.
func (s *LegStats) RecordPacket(jitterUS int64) {
	s.PacketsReceived.Add(1)
	s.JitterUS.Store(jitterUS)
	s.LastPacketTime.Store(time.Now().UnixNano())
}

// RecordRTT updates the round-trip time (RFC 3550 §6.4.1).
func (s *LegStats) RecordRTT(rttUS int64) {
	s.RTTUS.Store(rttUS)
}

// RecordLoss updates loss metrics from RTCP receiver report.
func (s *LegStats) RecordLoss(fractionLost uint8, cumulativeLost int64) {
	s.FractionLost.Store(uint32(fractionLost))
	s.CumulativeLost.Add(cumulativeLost)
}

// LegStatsSnapshot is a point-in-time copy of leg stats.
type LegStatsSnapshot struct {
	JitterUS        int64
	RTTUS           int64
	FractionLost    uint8
	CumulativeLost  int64
	PacketsReceived int64
	LastPacketTime  time.Time
}

func (s *LegStats) Snapshot() LegStatsSnapshot {
	lastNs := s.LastPacketTime.Load()
	var lastTime time.Time
	if lastNs > 0 {
		lastTime = time.Unix(0, lastNs)
	}
	return LegStatsSnapshot{
		JitterUS:        s.JitterUS.Load(),
		RTTUS:           s.RTTUS.Load(),
		FractionLost:    uint8(s.FractionLost.Load()),
		CumulativeLost:  s.CumulativeLost.Load(),
		PacketsReceived: s.PacketsReceived.Load(),
		LastPacketTime:  lastTime,
	}
}
