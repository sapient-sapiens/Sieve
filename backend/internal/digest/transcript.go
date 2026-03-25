package digest

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	transcriptTargetTurns  = 1
	transcriptContextTurns = 1
)

type ScoringWindow struct {
	Targets       []Paragraph
	ContextBefore string
	ContextAfter  string
}

// lineBounds returns [start, end) byte offsets for each line (split on '\n').
func lineBounds(s string) [][2]int {
	var out [][2]int
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, [2]int{start, i})
			start = i + 1
		}
	}
	out = append(out, [2]int{start, len(s)})
	return out
}

// isSpeakerLine returns true when a line looks like "Name: dialogue…" (podcast / interview transcripts).
func isSpeakerLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	colon := strings.IndexByte(line, ':')
	if colon <= 0 || colon > 100 {
		return false
	}
	name := strings.TrimSpace(line[:colon])
	if len(name) < 2 {
		return false
	}
	switch strings.ToLower(name) {
	case "http", "https", "ftp", "mailto":
		return false
	}
	r0, _ := utf8.DecodeRuneInString(name)
	if !unicode.IsLetter(r0) {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || r == '-' || r == '\'' || r == '\u2019' || r == '.' || r == ',' {
			continue
		}
		return false
	}
	return true
}

// isTimestampLine matches timed transcript exports like "0:066 seconds…" or "1:02 minutes…".
func isTimestampLine(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) < 4 {
		return false
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != ':' {
		return false
	}
	i++
	if i >= len(line) {
		return false
	}
	gotDigit := false
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		gotDigit = true
		i++
	}
	return gotDigit
}

func lineStartsTranscriptTurn(line string) bool {
	return isSpeakerLine(line) || isTimestampLine(line)
}

// SplitSpeakerTurns groups lines into paragraphs: each new line that looks like a speaker ("Name:")
// or a timed cue (digits:digits…) starts a turn. Continuation lines stay with the current turn.
func SplitSpeakerTurns(source string) []Paragraph {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	lines := lineBounds(source)
	var out []Paragraph
	var cur [][2]int
	idx := 0

	flush := func() {
		if len(cur) == 0 {
			return
		}
		rawStart := cur[0][0]
		rawEnd := cur[len(cur)-1][1]
		block := source[rawStart:rawEnd]
		t := strings.TrimSpace(block)
		if t == "" {
			cur = nil
			return
		}
		ls := trimLeftLen(block)
		rs := trimRightLen(block)
		sb := rawStart + ls
		eb := rawStart + rs
		out = append(out, Paragraph{
			Index:     idx,
			StartByte: sb,
			EndByte:   eb,
			Text:      source[sb:eb],
		})
		idx++
		cur = nil
	}

	for _, ln := range lines {
		line := source[ln[0]:ln[1]]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if lineStartsTranscriptTurn(line) {
			flush()
		}
		cur = append(cur, ln)
	}
	flush()
	return out
}

func countTurnStartLines(source string) int {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	n := 0
	for _, ln := range lineBounds(source) {
		line := source[ln[0]:ln[1]]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if lineStartsTranscriptTurn(line) {
			n++
		}
	}
	return n
}

func BuildScoringWindows(source string, paras []Paragraph, chunking string) []ScoringWindow {
	if chunking != ChunkingSpeakerLine {
		out := make([]ScoringWindow, 0, len(paras))
		for _, p := range paras {
			out = append(out, ScoringWindow{Targets: []Paragraph{p}})
		}
		return out
	}
	return BuildTranscriptScoringWindows(source, paras)
}

// BuildTranscriptScoringWindows groups raw speaker turns into 6-turn target windows while
// preserving a small context halo before and after each window for the LLM prompt.
func BuildTranscriptScoringWindows(source string, turns []Paragraph) []ScoringWindow {
	if len(turns) == 0 {
		return nil
	}
	var out []ScoringWindow
	for start := 0; start < len(turns); start += transcriptTargetTurns {
		end := start + transcriptTargetTurns
		if end > len(turns) {
			end = len(turns)
		}
		target := turns[start:end]
		beforeStart := start - transcriptContextTurns
		if beforeStart < 0 {
			beforeStart = 0
		}
		afterEnd := end + transcriptContextTurns
		if afterEnd > len(turns) {
			afterEnd = len(turns)
		}
		out = append(out, ScoringWindow{
			Targets:       append([]Paragraph(nil), target...),
			ContextBefore: joinParagraphText(source, turns[beforeStart:start]),
			ContextAfter:  joinParagraphText(source, turns[end:afterEnd]),
		})
	}
	return out
}

func joinParagraphText(source string, paras []Paragraph) string {
	if len(paras) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, p := range paras {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(source[p.StartByte:p.EndByte])
	}
	return sb.String()
}

// Speaker split modes (from API / jobs table).
const (
	SpeakerSplitAuto = "auto"
	SpeakerSplitOn   = "on"
	SpeakerSplitOff  = "off"
)

const (
	ChunkingBlankLine   = "blank_line"
	ChunkingSpeakerLine = "speaker_line"
)

// ChooseParagraphs picks blank-line vs speaker-line chunking.
func ChooseParagraphs(source string, blank []Paragraph, mode string) ([]Paragraph, string) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = SpeakerSplitAuto
	}

	switch mode {
	case SpeakerSplitOff:
		return blank, ChunkingBlankLine
	case SpeakerSplitOn:
		sp := SplitSpeakerTurns(source)
		if len(sp) >= 2 {
			return reindexParagraphs(sp), ChunkingSpeakerLine
		}
		return blank, ChunkingBlankLine
	default: // auto
		wc := WordCount(source)
		if wc < 200 {
			return blank, ChunkingBlankLine
		}
		sp := SplitSpeakerTurns(source)
		turns := countTurnStartLines(source)
		if len(sp) < 2 || (len(sp) < 8 && turns < 12) {
			return blank, ChunkingBlankLine
		}
		// Prefer transcript turns when they give much finer units than blank-line paragraphs
		// (e.g. timestamped export has 1–2 big blocks but hundreds of cue lines).
		if len(blank) == 1 {
			return reindexParagraphs(sp), ChunkingSpeakerLine
		}
		if len(sp) > len(blank)*4 {
			return reindexParagraphs(sp), ChunkingSpeakerLine
		}
		return blank, ChunkingBlankLine
	}
}

func reindexParagraphs(p []Paragraph) []Paragraph {
	for i := range p {
		p[i].Index = i
	}
	return p
}

