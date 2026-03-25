package digest

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: repo example.txt uses timestamp-prefixed lines (0:066 seconds…) — should chunk into many turns.
func TestExampleTxtManyParagraphs(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	p := filepath.Join(root, "example.txt")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("example.txt not found at %s: %v", p, err)
	}
	src := string(b)
	blank := SplitParagraphs(src)
	sp := SplitSpeakerTurns(src)
	t.Logf("blank-line paragraphs: %d, transcript turns: %d", len(blank), len(sp))
	if len(sp) < 50 {
		t.Fatalf("expected timestamp/speaker parser to produce many turns, got %d", len(sp))
	}
	paras, mode := ChooseParagraphs(src, blank, SpeakerSplitAuto)
	if mode != ChunkingSpeakerLine {
		t.Fatalf("auto mode should pick speaker_line for this file, got %s with %d paras", mode, len(paras))
	}
	if len(paras) < 50 {
		t.Fatalf("expected many scored units, got %d", len(paras))
	}
	windows := BuildTranscriptScoringWindows(src, paras)
	if transcriptTargetTurns > 1 && len(windows) >= len(paras) {
		t.Fatalf("expected grouped scoring windows, got %d vs %d", len(windows), len(paras))
	}
}
