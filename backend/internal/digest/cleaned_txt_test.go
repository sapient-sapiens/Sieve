package digest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: cleaned.txt is one speaker-prefixed line per row (Joe Rogan: / Pierre Poilievre:).
func TestCleanedTxtSpeakerChunking(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	p := filepath.Join(root, "cleaned.txt")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("cleaned.txt not found at %s: %v", p, err)
	}
	src := string(b)
	blank := SplitParagraphs(src)
	sp := SplitSpeakerTurns(src)
	turns := countTurnStartLines(src)

	t.Logf("blank-line paragraphs: %d, speaker turns: %d, turn-start lines: %d", len(blank), len(sp), turns)

	if len(blank) != 1 {
		t.Fatalf("expected single blank-line block (no \\n\\n between lines), got %d", len(blank))
	}
	if len(sp) < 100 {
		t.Fatalf("expected many speaker lines, got %d", len(sp))
	}
	if turns < 100 {
		t.Fatalf("expected many speaker/timestamp cues, got %d", turns)
	}

	paras, mode := ChooseParagraphs(src, blank, SpeakerSplitAuto)
	if mode != ChunkingSpeakerLine {
		t.Fatalf("auto should use transcript turns for cleaned.txt, got %s (%d paras)", mode, len(paras))
	}
	if len(paras) != len(sp) {
		t.Fatalf("ChooseParagraphs should expose raw transcript turns before grouping: %d vs %d", len(paras), len(sp))
	}
	windows := BuildTranscriptScoringWindows(src, paras)
	if transcriptTargetTurns > 1 && len(windows) >= len(paras) {
		t.Fatalf("scoring windows should be fewer than raw turns: %d vs %d", len(windows), len(paras))
	}
	if len(windows) > len(paras)/transcriptTargetTurns+2 {
		t.Fatalf("unexpectedly high scoring window count: %d", len(windows))
	}

	// Offsets should land inside source and cover trimmed text only.
	for i, para := range paras {
		if para.StartByte < 0 || para.EndByte > len(src) || para.StartByte >= para.EndByte {
			t.Fatalf("para %d: bad offsets [%d,%d) len=%d", i, para.StartByte, para.EndByte, len(src))
		}
		got := src[para.StartByte:para.EndByte]
		if got != para.Text {
			t.Fatalf("para %d: text mismatch", i)
		}
		if !lineStartsTranscriptTurn(stringsFirstLine(para.Text)) {
			t.Fatalf("para %d: expected first line to start a transcript turn, got %q", i, truncate(para.Text, 80))
		}
	}
	for i, window := range windows {
		if len(window.Targets) == 0 {
			t.Fatalf("window %d has no targets", i)
		}
		for _, para := range window.Targets {
			if para.StartByte < 0 || para.EndByte > len(src) || para.StartByte >= para.EndByte {
				t.Fatalf("window %d target bad offsets [%d,%d) len=%d", i, para.StartByte, para.EndByte, len(src))
			}
		}
		if i > 0 && strings.TrimSpace(window.ContextBefore) == "" {
			t.Fatalf("window %d should have before-context", i)
		}
	}
}

func stringsFirstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
