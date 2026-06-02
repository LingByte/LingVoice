package utils

import (
	"regexp"
	"strings"
)

// CleanTextOptions controls optional document text normalization before chunking or indexing.
type CleanTextOptions struct {
	StripMarkdown bool
	DedupLines    bool
}

var (
	mdHeading   = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	mdBold      = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalic    = regexp.MustCompile(`(?m)(^|[^*])\*([^*]+)\*([^*]|$)`)
	mdLink      = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdCodeFence = regexp.MustCompile("(?s)```.*?```")
	mdInline    = regexp.MustCompile("`([^`]+)`")
)

// CleanText normalizes raw document text (strip markdown, deduplicate lines, etc.).
func CleanText(text string, opts *CleanTextOptions) string {
	text = strings.TrimSpace(text)
	if text == "" || opts == nil {
		return text
	}
	if opts.StripMarkdown {
		text = mdCodeFence.ReplaceAllString(text, " ")
		text = mdHeading.ReplaceAllString(text, "")
		text = mdLink.ReplaceAllString(text, "$1")
		text = mdBold.ReplaceAllString(text, "$1")
		text = mdItalic.ReplaceAllString(text, "$1$2$3")
		text = mdInline.ReplaceAllString(text, "$1")
	}
	if opts.DedupLines {
		lines := strings.Split(text, "\n")
		out := lines[:0]
		var prev string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				if len(out) > 0 && out[len(out)-1] != "" {
					out = append(out, "")
				}
				prev = ""
				continue
			}
			if line == prev {
				continue
			}
			out = append(out, line)
			prev = line
		}
		text = strings.TrimSpace(strings.Join(out, "\n"))
	}
	return strings.TrimSpace(text)
}

// DefaultPreChunkClean returns common defaults for LLM chunking prep.
func DefaultPreChunkClean() *CleanTextOptions {
	return &CleanTextOptions{StripMarkdown: true, DedupLines: true}
}
