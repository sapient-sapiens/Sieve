package digest

import (
	"strings"
	"testing"

	"interest-digest/internal/llm"
)

func TestEnsureAtLeastOneKept(t *testing.T) {
	paras := []Paragraph{
		{Index: 0, StartByte: 0, EndByte: 2, Text: "a"},
		{Index: 1, StartByte: 4, EndByte: 5, Text: "b"},
	}
	dm := map[int]llm.ParagraphDecision{
		0: {ParagraphIndex: 0, Action: "omit", Reason: "test"},
		1: {ParagraphIndex: 1, Action: "omit", Reason: "test"},
	}
	ensureAtLeastOneKept(dm, paras)
	if !strings.EqualFold(dm[0].Action, "keep") {
		t.Fatalf("expected index 0 kept, got %#v", dm[0])
	}
	if strings.EqualFold(dm[1].Action, "keep") {
		t.Fatalf("expected index 1 still omit, got %#v", dm[1])
	}
}

func TestEnsureAtLeastOneKeptNoOpWhenAlreadyKept(t *testing.T) {
	paras := []Paragraph{{Index: 0, Text: "x"}}
	dm := map[int]llm.ParagraphDecision{
		0: {ParagraphIndex: 0, Action: "keep", Reason: "ok"},
	}
	ensureAtLeastOneKept(dm, paras)
	if dm[0].Reason != "ok" {
		t.Fatalf("should not change existing keep: %#v", dm[0])
	}
}

func TestBoundedWorkerCount(t *testing.T) {
	if got := boundedWorkerCount(10, 0, 4); got != 4 {
		t.Fatalf("default workers: got %d", got)
	}
	if got := boundedWorkerCount(2, 8, 4); got != 2 {
		t.Fatalf("should cap by total batches: got %d", got)
	}
	if got := boundedWorkerCount(10, -1, 0); got != 1 {
		t.Fatalf("should keep at least one worker: got %d", got)
	}
}

func TestBuildScoreBatches(t *testing.T) {
	windows := make([]ScoringWindow, 0, 50)
	idx := 0
	for i := 0; i < 50; i++ {
		targets := []Paragraph{}
		for j := 0; j < 3; j++ {
			targets = append(targets, Paragraph{
				Index: idx,
				Text:  strings.Repeat("x", 200),
			})
			idx++
		}
		windows = append(windows, ScoringWindow{
			Targets:       targets,
			ContextBefore: strings.Repeat("b", 100),
			ContextAfter:  strings.Repeat("a", 100),
		})
	}
	batches := buildScoreBatches(windows)
	if len(batches) < 2 {
		t.Fatalf("expected multiple batches, got %d", len(batches))
	}
	for i, batch := range batches {
		targets := 0
		for _, item := range batch {
			targets += len(item.Targets)
		}
		if targets > maxBatchTargets {
			t.Fatalf("batch %d too large: %d", i, len(batch))
		}
		chars := 0
		for _, item := range batch {
			chars += len(item.ContextBefore) + len(item.ContextAfter)
			for _, target := range item.Targets {
				chars += len(target.Text)
			}
		}
		if chars > maxBatchChars {
			t.Fatalf("batch %d too many chars: %d", i, chars)
		}
	}
}

