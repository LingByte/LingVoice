// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package utils

import "strings"

// SplitHTMLBodyForTranslation splits full HTML into: prefix (through opening <body…>), inner (body children only), suffix (from </body> onward).
// If there is no <body>…</body> pair, returns prefix="", inner=full, suffix="" so callers may translate the whole fragment.
func SplitHTMLBodyForTranslation(s string) (prefix, inner, suffix string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", ""
	}
	low := strings.ToLower(s)
	bi := strings.Index(low, "<body")
	if bi < 0 {
		return "", s, ""
	}
	rest := s[bi:]
	gt := strings.Index(rest, ">")
	if gt < 0 {
		return "", s, ""
	}
	openEnd := bi + gt + 1
	prefix = s[:openEnd]
	tail := s[openEnd:]
	ci := strings.Index(strings.ToLower(tail), "</body>")
	if ci < 0 {
		// Unclosed body: treat remainder as inner (no suffix)
		return prefix, tail, ""
	}
	inner = tail[:ci]
	suffix = tail[ci:]
	return prefix, inner, suffix
}

// JoinHTMLBodyAfterTranslation reverses SplitHTMLBodyForTranslation.
func JoinHTMLBodyAfterTranslation(prefix, translatedInner, suffix string) string {
	return prefix + translatedInner + suffix
}
