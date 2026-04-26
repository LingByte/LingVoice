// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import "testing"

func TestSpeechBillableSeconds(t *testing.T) {
	if v := speechBillableSeconds(1000, 32000, 32000); v != 1.0 {
		t.Fatalf("equal wall and est: got %v", v)
	}
	if v := speechBillableSeconds(100, 320000, 32000); v != 10.0 {
		t.Fatalf("byte est dominates: got %v want 10", v)
	}
	if v := speechBillableSeconds(5000, 0, 32000); v != 5.0 {
		t.Fatalf("wall only: got %v", v)
	}
}

func TestSpeechOpenAPIQuotaDelta(t *testing.T) {
	if d := speechOpenAPIQuotaDelta(false, "default", 10, 2, 1); d != 0 {
		t.Fatalf("fail: got %d", d)
	}
	if d := speechOpenAPIQuotaDelta(true, "default", 2.5, 10, 1); d != 25 {
		t.Fatalf("2.5s*10: got %d want 25", d)
	}
}
