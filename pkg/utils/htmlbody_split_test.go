// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package utils

import (
	"strings"
	"testing"
)

func TestSplitHTMLBodyForTranslation(t *testing.T) {
	full := `<!DOCTYPE html><html><head><style>.x{}</style></head><body class="a"><p>Hi</p></body></html>`
	pre, in, suf := SplitHTMLBodyForTranslation(full)
	if !strings.Contains(strings.ToLower(pre), "<body") || !strings.HasSuffix(pre, ">") {
		t.Fatalf("prefix: %q", pre)
	}
	if in != `<p>Hi</p>` {
		t.Fatalf("inner: %q", in)
	}
	if !strings.HasPrefix(strings.ToLower(suf), "</body>") {
		t.Fatalf("suffix: %q", suf)
	}
	if JoinHTMLBodyAfterTranslation(pre, in, suf) != full {
		t.Fatal("join mismatch")
	}
}

func TestSplitHTMLBodyForTranslationNoBody(t *testing.T) {
	frag := `<p>Only</p>`
	pre, in, suf := SplitHTMLBodyForTranslation(frag)
	if pre != "" || suf != "" || in != frag {
		t.Fatalf("got %q %q %q", pre, in, suf)
	}
}
