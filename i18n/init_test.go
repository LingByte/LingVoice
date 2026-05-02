// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package i18n

import "testing"

func TestInitEmbeddedLocales(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("i18n.Init (embedded YAML): %v", err)
	}
}
