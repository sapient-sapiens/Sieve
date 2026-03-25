package digest

import (
	"strings"
)

// Paragraph is a slice of source text with byte offsets into the original UTF-8 string.
type Paragraph struct {
	Index     int
	StartByte int
	EndByte   int
	Text      string
}

// SplitParagraphs splits on blank lines (\n\n+). Offsets refer to trimmed paragraph content inside the source.
func SplitParagraphs(source string) []Paragraph {
	s := strings.ReplaceAll(source, "\r\n", "\n")
	var out []Paragraph
	idx := 0
	n := len(s)
	i := 0
	for i < n {
		for i < n && (s[i] == '\n' || s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		j := i
		for j < n {
			if j+1 < n && s[j] == '\n' && s[j+1] == '\n' {
				break
			}
			j++
		}
		block := s[i:j]
		t := strings.TrimSpace(block)
		if t != "" {
			ls := trimLeftLen(block)
			rs := trimRightLen(block)
			sb := i + ls
			eb := i + rs
			out = append(out, Paragraph{Index: idx, StartByte: sb, EndByte: eb, Text: t})
			idx++
		}
		if j+1 < n && s[j] == '\n' && s[j+1] == '\n' {
			i = j + 2
		} else {
			i = j
		}
	}
	return out
}

func WordCount(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return len(strings.Fields(s))
}

// WholeSourceParagraph treats the entire non-empty string as one paragraph (byte offsets for trimmed content).
func WholeSourceParagraph(source string) (Paragraph, bool) {
	t := strings.TrimSpace(source)
	if t == "" {
		return Paragraph{}, false
	}
	ls := trimLeftLen(source)
	rs := trimRightLen(source)
	return Paragraph{Index: 0, StartByte: ls, EndByte: rs, Text: t}, true
}

func trimLeftLen(b string) int {
	k := 0
	for k < len(b) {
		c := b[k]
		if c == ' ' || c == '\t' || c == '\n' {
			k++
			continue
		}
		break
	}
	return k
}

func trimRightLen(b string) int {
	k := len(b)
	for k > 0 {
		c := b[k-1]
		if c == ' ' || c == '\t' || c == '\n' {
			k--
			continue
		}
		break
	}
	return k
}
