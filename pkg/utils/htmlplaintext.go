// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package utils

import (
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var multiSpace = regexp.MustCompile(`[ \t\f\r]+`)
var multiNL = regexp.MustCompile(`\n{3,}`)

// HTMLToPlainText strips tags and style-bearing markup, keeps approximate line breaks, preserves {{.Var}} text.
func HTMLToPlainText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() == io.EOF {
				break
			}
			break
		}
		switch tt {
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken:
			tn, _ := z.TagName()
			t := string(tn)
			switch t {
			case "script", "style", "noscript":
				skip++
			case "br":
				b.WriteByte('\n')
			case "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "title", "blockquote", "pre", "section", "article", "header", "footer", "table", "thead", "tbody":
				if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
					b.WriteByte('\n')
				}
			}
		case html.EndTagToken:
			tn, _ := z.TagName()
			t := string(tn)
			switch t {
			case "script", "style", "noscript":
				if skip > 0 {
					skip--
				}
			case "p", "div", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "section", "article", "header", "footer", "table":
				b.WriteByte('\n')
			}
		}
	}
	out := strings.TrimSpace(b.String())
	out = multiSpace.ReplaceAllString(out, " ")
	out = multiNL.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}
