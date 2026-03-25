package digest

import (
	"strings"
	"testing"
)

func TestSplitParagraphs(t *testing.T) {
	src := "A line.\n\nSecond para.\n\n\nThird."
	p := SplitParagraphs(src)
	if len(p) != 3 {
		t.Fatalf("want 3 paras, got %d", len(p))
	}
	if p[0].Text != "A line." || p[0].Index != 0 {
		t.Fatalf("first para: %#v", p[0])
	}
	if !strings.Contains(src[p[1].StartByte:p[1].EndByte], "Second") {
		t.Fatalf("offsets wrong for second: %q", src[p[1].StartByte:p[1].EndByte])
	}
}

func TestWholeSourceParagraph(t *testing.T) {
	src := "  hello world  "
	p, ok := WholeSourceParagraph(src)
	if !ok || p.Text != "hello world" {
		t.Fatalf("got %#v ok=%v", p, ok)
	}
}
