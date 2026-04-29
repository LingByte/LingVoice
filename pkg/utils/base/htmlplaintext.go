// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package base

import (
	"encoding/json"
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

var mailTemplateVarRe = regexp.MustCompile(`\{\{\s*\.?([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// DeriveTemplateVariables 从 HTML / 纯文本中解析 {{.Name}}、{{Name}} 占位符，生成 JSON 数组写入 variables。
func DeriveTemplateVariables(html, plain string) string {
	text := html + "\n" + plain
	seen := map[string]struct{}{}
	var names []string
	for _, m := range mailTemplateVarRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		k := m[1]
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		names = append(names, k)
	}
	b, _ := json.Marshal(names)
	return string(b)
}

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
