package utils

import "testing"

func TestCleanText_StripMarkdown(t *testing.T) {
	in := "# Title\n\n**bold** and [link](http://x.com)"
	out := CleanText(in, &CleanTextOptions{StripMarkdown: true})
	if out == in || out == "" {
		t.Fatalf("unexpected: %q", out)
	}
}

func TestCleanText_DedupLines(t *testing.T) {
	in := "a\na\nb\n\nb"
	out := CleanText(in, &CleanTextOptions{DedupLines: true})
	if out != "a\nb\n\nb" {
		t.Fatalf("got %q", out)
	}
}
