// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package base

import (
	"strings"
	"testing"
)

func TestHTMLToPlainText(t *testing.T) {
	const in = `<div style="color:red"><p>Hi <b>{{.Name}}</b></p><style>.x{}</style><script>evil()</script><br/>Line2</div>`
	got := HTMLToPlainText(in)
	if !strings.Contains(got, "{{.Name}}") {
		t.Fatalf("missing placeholder: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "script") || strings.Contains(got, "evil") {
		t.Fatalf("script leaked: %q", got)
	}
	if !strings.Contains(got, "Hi") || !strings.Contains(got, "Line2") {
		t.Fatalf("unexpected: %q", got)
	}
}
