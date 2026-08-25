package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"testing"
	"time"
)

func TestTelemetry_RecordRx(t *testing.T) {
	tel := Telemetry()
	tel.Reset()

	tel.RecordRx(10, 2, 1, 1600)
	snap := tel.Snapshot()

	if snap.RxPackets != 10 {
		t.Errorf("RxPackets = %d, want 10", snap.RxPackets)
	}
	if snap.RxLost != 2 {
		t.Errorf("RxLost = %d, want 2", snap.RxLost)
	}
	if snap.RxInternalDrops != 1 {
		t.Errorf("RxInternalDrops = %d, want 1", snap.RxInternalDrops)
	}
	if snap.BytesReceived != 1600 {
		t.Errorf("BytesReceived = %d, want 1600", snap.BytesReceived)
	}

	tel.Reset()
}

func TestTelemetry_RecordTx(t *testing.T) {
	tel := Telemetry()
	tel.Reset()

	tel.RecordTx(20, 3, 3200)
	snap := tel.Snapshot()

	if snap.TxPackets != 20 {
		t.Errorf("TxPackets = %d, want 20", snap.TxPackets)
	}
	if snap.TxDropped != 3 {
		t.Errorf("TxDropped = %d, want 3", snap.TxDropped)
	}
	if snap.BytesSent != 3200 {
		t.Errorf("BytesSent = %d, want 3200", snap.BytesSent)
	}

	tel.Reset()
}

func TestTelemetry_RegisterSession(t *testing.T) {
	tel := Telemetry()
	tel.Reset()

	tel.RegisterSession()
	tel.RegisterSession()
	if snap := tel.Snapshot(); snap.ActiveSessions != 2 {
		t.Errorf("after 2 registers ActiveSessions = %d, want 2", snap.ActiveSessions)
	}

	tel.UnregisterSession()
	if snap := tel.Snapshot(); snap.ActiveSessions != 1 {
		t.Errorf("after 1 unregister ActiveSessions = %d, want 1", snap.ActiveSessions)
	}

	tel.UnregisterSession()
	if snap := tel.Snapshot(); snap.ActiveSessions != 0 {
		t.Errorf("after 2 unregisters ActiveSessions = %d, want 0", snap.ActiveSessions)
	}

	tel.Reset()
}

func TestTelemetry_Reset(t *testing.T) {
	tel := Telemetry()
	tel.Reset()

	tel.RecordRx(5, 1, 1, 800)
	tel.RecordTx(7, 2, 1120)
	tel.RegisterSession()

	tel.Reset()
	snap := tel.Snapshot()

	if snap.ActiveSessions != 0 {
		t.Errorf("ActiveSessions = %d, want 0 after reset", snap.ActiveSessions)
	}
	if snap.RxPackets != 0 {
		t.Errorf("RxPackets = %d, want 0 after reset", snap.RxPackets)
	}
	if snap.RxLost != 0 {
		t.Errorf("RxLost = %d, want 0 after reset", snap.RxLost)
	}
	if snap.RxInternalDrops != 0 {
		t.Errorf("RxInternalDrops = %d, want 0 after reset", snap.RxInternalDrops)
	}
	if snap.TxPackets != 0 {
		t.Errorf("TxPackets = %d, want 0 after reset", snap.TxPackets)
	}
	if snap.TxDropped != 0 {
		t.Errorf("TxDropped = %d, want 0 after reset", snap.TxDropped)
	}
	if snap.BytesReceived != 0 {
		t.Errorf("BytesReceived = %d, want 0 after reset", snap.BytesReceived)
	}
	if snap.BytesSent != 0 {
		t.Errorf("BytesSent = %d, want 0 after reset", snap.BytesSent)
	}
}

func TestLegStats_RecordPacket(t *testing.T) {
	s := NewLegStats()

	s.RecordPacket(500)
	s.RecordPacket(750)
	s.RecordPacket(1000)

	snap := s.Snapshot()
	if snap.PacketsReceived != 3 {
		t.Errorf("PacketsReceived = %d, want 3", snap.PacketsReceived)
	}
	if snap.JitterUS != 1000 {
		t.Errorf("JitterUS = %d, want 1000 (last value)", snap.JitterUS)
	}
	if snap.LastPacketTime.IsZero() {
		t.Error("LastPacketTime is zero, want a valid timestamp")
	}
	if time.Since(snap.LastPacketTime) > time.Second {
		t.Errorf("LastPacketTime too old: %v", snap.LastPacketTime)
	}
}

func TestLegStats_RecordRTT(t *testing.T) {
	s := NewLegStats()

	s.RecordRTT(25000)
	if snap := s.Snapshot(); snap.RTTUS != 25000 {
		t.Errorf("RTTUS = %d, want 25000", snap.RTTUS)
	}

	s.RecordRTT(40000)
	if snap := s.Snapshot(); snap.RTTUS != 40000 {
		t.Errorf("RTTUS = %d, want 40000 (overwritten)", snap.RTTUS)
	}
}

func TestLegStats_RecordLoss(t *testing.T) {
	s := NewLegStats()

	s.RecordLoss(42, 10)
	snap := s.Snapshot()
	if snap.FractionLost != 42 {
		t.Errorf("FractionLost = %d, want 42", snap.FractionLost)
	}
	if snap.CumulativeLost != 10 {
		t.Errorf("CumulativeLost = %d, want 10", snap.CumulativeLost)
	}

	s.RecordLoss(128, 5)
	snap = s.Snapshot()
	if snap.FractionLost != 128 {
		t.Errorf("FractionLost = %d, want 128 (overwritten)", snap.FractionLost)
	}
	if snap.CumulativeLost != 15 {
		t.Errorf("CumulativeLost = %d, want 15 (accumulated)", snap.CumulativeLost)
	}
}

func TestLegStats_Snapshot(t *testing.T) {
	s := NewLegStats()

	s.RecordPacket(300)
	s.RecordPacket(600)
	s.RecordRTT(15000)
	s.RecordLoss(64, 8)

	snap := s.Snapshot()

	if snap.PacketsReceived != 2 {
		t.Errorf("PacketsReceived = %d, want 2", snap.PacketsReceived)
	}
	if snap.JitterUS != 600 {
		t.Errorf("JitterUS = %d, want 600", snap.JitterUS)
	}
	if snap.RTTUS != 15000 {
		t.Errorf("RTTUS = %d, want 15000", snap.RTTUS)
	}
	if snap.FractionLost != 64 {
		t.Errorf("FractionLost = %d, want 64", snap.FractionLost)
	}
	if snap.CumulativeLost != 8 {
		t.Errorf("CumulativeLost = %d, want 8", snap.CumulativeLost)
	}
	if snap.LastPacketTime.IsZero() {
		t.Error("LastPacketTime is zero, want a valid timestamp")
	}
}
