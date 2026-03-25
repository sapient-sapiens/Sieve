package digest

import (
	"strings"
	"testing"
)

func TestSplitSpeakerTurns(t *testing.T) {
	src := `Joe Rogan: Train by day.
All day.

Pierre Poilievre: It's great to be here.
Thanks for having me.

Joe Rogan: Yeah, I know.`
	p := SplitSpeakerTurns(src)
	if len(p) != 3 {
		t.Fatalf("want 3 turns, got %d", len(p))
	}
	if !strings.Contains(p[0].Text, "Joe Rogan:") || !strings.Contains(p[0].Text, "Train by day") {
		t.Fatalf("first turn: %#v", p[0])
	}
	if p[0].Index != 0 || p[1].Index != 1 {
		t.Fatalf("indices: %#v %#v", p[0], p[1])
	}
}

func TestChooseParagraphsAutoUsesSpeaker(t *testing.T) {
	line := "Joe Rogan: hello world one two three four five.\nPierre Poilievre: hi there six seven eight nine ten.\n"
	src := strings.Repeat(line, 50)
	blank := SplitParagraphs(src)
	if len(blank) != 1 {
		t.Fatalf("blank split should be 1 block, got %d", len(blank))
	}
	if WordCount(blank[0].Text) < 200 {
		t.Fatalf("fixture should be long enough for auto heuristic, wc=%d", WordCount(blank[0].Text))
	}
	paras, mode := ChooseParagraphs(src, blank, SpeakerSplitAuto)
	if mode != ChunkingSpeakerLine {
		t.Fatalf("want speaker_line, got %s", mode)
	}
	if len(paras) < 8 {
		t.Fatalf("want many speaker turns, got %d", len(paras))
	}
}

func TestBuildTranscriptScoringWindows(t *testing.T) {
	var lines []string
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			lines = append(lines, "Joe Rogan: line number "+strings.Repeat("a", i+1))
		} else {
			lines = append(lines, "Pierre Poilievre: reply number "+strings.Repeat("b", i+1))
		}
	}
	src := strings.Join(lines, "\n")
	turns := SplitSpeakerTurns(src)
	windows := BuildTranscriptScoringWindows(src, turns)
	if len(windows) != len(turns) {
		t.Fatalf("want %d scoring windows, got %d", len(turns), len(windows))
	}
	for i, window := range windows {
		if len(window.Targets) != transcriptTargetTurns {
			t.Fatalf("window %d target count = %d", i, len(window.Targets))
		}
		if window.Targets[0].Index != i {
			t.Fatalf("window %d target index mismatch: %#v", i, window.Targets)
		}
		if i == 0 {
			if strings.TrimSpace(window.ContextBefore) != "" {
				t.Fatalf("first window should not have prior context: %q", window.ContextBefore)
			}
		} else if !strings.Contains(window.ContextBefore, lines[i-1]) {
			t.Fatalf("window %d missing before-context: %q", i, window.ContextBefore)
		}
		if i == len(windows)-1 {
			if strings.TrimSpace(window.ContextAfter) != "" {
				t.Fatalf("last window should not have after-context: %q", window.ContextAfter)
			}
		} else if !strings.Contains(window.ContextAfter, lines[i+1]) {
			t.Fatalf("window %d missing after-context: %q", i, window.ContextAfter)
		}
	}
}

func TestChooseParagraphsOff(t *testing.T) {
	src := "Joe Rogan: x.\nPierre: y.\n"
	blank := SplitParagraphs(src)
	paras, mode := ChooseParagraphs(src, blank, SpeakerSplitOff)
	if mode != ChunkingBlankLine || len(paras) != 1 {
		t.Fatalf("got mode=%s len=%d", mode, len(paras))
	}
}

func TestTimestampLine(t *testing.T) {
	if !isTimestampLine("0:066 secondsTrain by day.") {
		t.Fatal()
	}
	if !isTimestampLine("  1:02 minutes, 2 secondshello") {
		t.Fatal()
	}
	if isTimestampLine("Joe Rogan: hi.") {
		t.Fatal("speaker line should not be timestamp")
	}
}

func TestSpeakerLineWithDigits(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"Speaker 1: hello world", true},
		{"Speaker 2: hi there", true},
		{"Guest 3: some text", true},
		{"Joe Rogan: test", true},
		{"http://example.com", false},
		{"12:00something", false},
		{"0:06hello", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isSpeakerLine(tc.line)
		if got != tc.want {
			t.Errorf("isSpeakerLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestChooseParagraphsAutoWithNumberedSpeakers(t *testing.T) {
	line := "Speaker 1: hello world one two three four five.\nSpeaker 2: hi there six seven eight nine ten.\n"
	src := strings.Repeat(line, 50)
	blank := SplitParagraphs(src)
	paras, mode := ChooseParagraphs(src, blank, SpeakerSplitAuto)
	if mode != ChunkingSpeakerLine {
		t.Fatalf("want speaker_line for Speaker 1/2 format, got %s", mode)
	}
	if len(paras) < 8 {
		t.Fatalf("want many speaker turns, got %d", len(paras))
	}
}
