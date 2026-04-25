// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package utils

import "testing"

func TestRenderMailHTML(t *testing.T) {
	out, err := RenderMailHTML(`<p>Hi {{.User}}, code {{.Code}}</p>`, map[string]any{"User": "A", "Code": "99"})
	if err != nil {
		t.Fatal(err)
	}
	want := `<p>Hi A, code 99</p>`
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestRenderMailHTML_missingKey(t *testing.T) {
	out, err := RenderMailHTML(`<p>{{.Only}}</p>`, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out != `<p></p>` {
		t.Fatalf("got %q", out)
	}
}

func TestRenderMailText(t *testing.T) {
	out, err := RenderMailText(`验证码 {{.Code}}`, map[string]any{"Code": "123456"})
	if err != nil {
		t.Fatal(err)
	}
	if out != `验证码 123456` {
		t.Fatalf("got %q", out)
	}
}
