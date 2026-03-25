package digest

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"interest-digest/internal/llm"
)

type Pipeline struct {
	LLM              *llm.Client
	ScoreConcurrency int
}

// Run builds a DigestDocument from source text and reader preferences.
// speakerSplitMode is "auto" | "on" | "off" (see digest.SpeakerSplit*).
// onProgress is optional; phase is "scoring" (step/total = completed LLM batches) or "assembling" (final span build).
func (p *Pipeline) Run(ctx context.Context, jobID, source, preferences string, speakerSplitMode string, onProgress func(phase string, step, total int)) (*DigestDocument, error) {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	blank := SplitParagraphs(source)
	if p, ok := WholeSourceParagraph(source); len(blank) == 0 && ok {
		blank = []Paragraph{p}
	}
	if len(blank) == 0 {
		return nil, fmt.Errorf("no paragraphs found; paste non-empty text (blank lines between paragraphs work best)")
	}
	paras, chunking := ChooseParagraphs(source, blank, speakerSplitMode)
	if len(paras) == 0 {
		return nil, fmt.Errorf("no paragraphs found after chunking")
	}
	scoringWindows := BuildScoringWindows(source, paras, chunking)
	if len(scoringWindows) == 0 {
		return nil, fmt.Errorf("no scoring windows found after chunking")
	}

	decisionMap, err := p.scoreAll(ctx, preferences, scoringWindows, onProgress)
	if err != nil {
		return nil, err
	}

	for _, para := range paras {
		if _, ok := decisionMap[para.Index]; !ok {
			decisionMap[para.Index] = llm.ParagraphDecision{ParagraphIndex: para.Index, Action: "keep", Reason: "default"}
		}
	}
	ensureAtLeastOneKept(decisionMap, paras)

	assemblingTotal := 1
	assemblingStep := 0
	reportAssemblingProgress := func() {
		if onProgress != nil {
			onProgress("assembling", assemblingStep, assemblingTotal)
		}
	}
	reportAssemblingProgress()

	mainSpans, omitted := assembleDigestSpans(source, paras, decisionMap)
	if len(mainSpans) == 0 {
		return nil, fmt.Errorf("internal error: digest has no readable spans after assembly")
	}

	doc := &DigestDocument{
		JobID:           jobID,
		Status:          "completed",
		SourceText:      source,
		WordCountBefore: WordCount(source),
		ChunkingMode:    chunking,
		Spans:           mainSpans,
		OmittedRanges:   omitted,
	}
	doc.WordCountAfter = countDigestWords(mainSpans)
	assemblingStep = assemblingTotal
	reportAssemblingProgress()
	return doc, nil
}

func countDigestWords(spans []Span) int {
	n := 0
	for _, sp := range spans {
		if sp.Type == SpanSourceKept {
			n += WordCount(sp.Text)
		}
	}
	return n
}

func hasRange(rr []OmittedRange, start, end int) bool {
	for _, x := range rr {
		if x.Start == start && x.End == end {
			return true
		}
	}
	return false
}

func buildOmittedRange(source string, paras []Paragraph, dm map[int]llm.ParagraphDecision, fromIdx, toIdx int) *OmittedRange {
	if fromIdx > toIdx {
		return nil
	}
	var sel []Paragraph
	for _, p := range paras {
		if p.Index < fromIdx || p.Index > toIdx {
			continue
		}
		if strings.EqualFold(dm[p.Index].Action, "omit") {
			sel = append(sel, p)
		}
	}
	if len(sel) == 0 {
		return nil
	}
	sort.Slice(sel, func(i, j int) bool { return sel[i].Index < sel[j].Index })
	startB := sel[0].StartByte
	endB := sel[len(sel)-1].EndByte
	var reasons []string
	seen := map[string]struct{}{}
	for _, p := range sel {
		r := strings.TrimSpace(dm[p.Index].Reason)
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		if len(reasons) < 2 {
			reasons = append(reasons, r)
		}
	}
	reason := strings.Join(reasons, "; ")
	if reason == "" {
		reason = "omitted for this reader"
	} else if extra := len(seen) - len(reasons); extra > 0 {
		reason = fmt.Sprintf("%s; and %d more", reason, extra)
	}
	return &OmittedRange{
		Start:     startB,
		End:       endB,
		Reason:    reason,
		WordCount: WordCount(source[startB:endB]),
	}
}

// ensureAtLeastOneKept upgrades one paragraph to keep when the model omitted everything,
// so the reader always gets a non-empty digest.
func ensureAtLeastOneKept(decisionMap map[int]llm.ParagraphDecision, paras []Paragraph) {
	for _, p := range paras {
		if strings.EqualFold(decisionMap[p.Index].Action, "keep") {
			return
		}
	}
	if len(paras) == 0 {
		return
	}
	bestIdx := paras[0].Index
	prev := decisionMap[bestIdx]
	reason := "kept so the digest is not empty — filters would remove all paragraphs; try broadening what you keep or clearing avoid rules"
	if r := strings.TrimSpace(prev.Reason); r != "" {
		reason = fmt.Sprintf("kept so the digest is not empty (was omitted: %s); consider broadening keep or clearing avoid", r)
	}
	decisionMap[bestIdx] = llm.ParagraphDecision{
		ParagraphIndex: bestIdx,
		Action:         "keep",
		Reason:         reason,
	}
}

// assembleDigestSpans builds reading-order spans from all paragraphs and their decisions.
func assembleDigestSpans(source string, paras []Paragraph, decisionMap map[int]llm.ParagraphDecision) ([]Span, []OmittedRange) {
	spanID := 0
	nextID := func() string {
		spanID++
		return "s" + strconv.Itoa(spanID)
	}

	var omitted []OmittedRange
	var mainSpans []Span
	var lastKeptIdx *int

	const minMarkerWords = 60

	for i := 0; i < len(paras); i++ {
		para := paras[i]
		dec := decisionMap[para.Index]
		keep := strings.EqualFold(dec.Action, "keep")
		if !keep {
			continue
		}

		if lastKeptIdx == nil {
			if i > 0 {
				if ob := buildOmittedRange(source, paras, decisionMap, paras[0].Index, paras[i-1].Index); ob != nil {
					omitted = append(omitted, *ob)
					wc := WordCount(source[ob.Start:ob.End])
					if wc >= minMarkerWords {
						mainSpans = append(mainSpans, Span{
							ID:     nextID(),
							Type:   SpanTruncationMarker,
							Text:   fmt.Sprintf(" ··· ~%d words omitted ··· ", max(1, wc)),
							Reason: ob.Reason,
						})
					}
				}
			}
		} else {
			hasOmitBetween := false
			for k := *lastKeptIdx + 1; k < i; k++ {
				if strings.EqualFold(decisionMap[paras[k].Index].Action, "omit") {
					hasOmitBetween = true
					break
				}
			}
			if hasOmitBetween {
				ob := buildOmittedRange(source, paras, decisionMap, paras[*lastKeptIdx+1].Index, paras[i-1].Index)
				if ob != nil {
					omitted = append(omitted, *ob)
					wc := WordCount(source[ob.Start:ob.End])
					if wc >= minMarkerWords {
						mainSpans = append(mainSpans, Span{
							ID:     nextID(),
							Type:   SpanTruncationMarker,
							Text:   fmt.Sprintf(" ··· ~%d words omitted ··· ", max(1, wc)),
							Reason: ob.Reason,
						})
					}
				}
			}
		}

		text := source[para.StartByte:para.EndByte]
		mainSpans = append(mainSpans, Span{
			ID:          nextID(),
			Type:        SpanSourceKept,
			Text:        text,
			StartOffset: PtrInt(para.StartByte),
			EndOffset:   PtrInt(para.EndByte),
		})
		li := i
		lastKeptIdx = &li
	}

	if lastKeptIdx != nil {
		last := paras[*lastKeptIdx]
		if ob := buildOmittedRange(source, paras, decisionMap, last.Index+1, paras[len(paras)-1].Index); ob != nil {
			if !hasRange(omitted, ob.Start, ob.End) {
				omitted = append(omitted, *ob)
				wc := WordCount(source[ob.Start:ob.End])
				if wc >= minMarkerWords {
					mainSpans = append(mainSpans, Span{
						ID:     nextID(),
						Type:   SpanTruncationMarker,
						Text:   fmt.Sprintf(" ··· ~%d words omitted (end) ··· ", max(1, wc)),
						Reason: ob.Reason,
					})
				}
			}
		}
	}

	return mainSpans, omitted
}

const (
	maxBatchTargets = 48
	maxBatchChars   = 16000
)

func (p *Pipeline) scoreAll(ctx context.Context, preferences string, windows []ScoringWindow, onProgress func(phase string, step, total int)) (map[int]llm.ParagraphDecision, error) {
	if p.LLM == nil {
		return nil, fmt.Errorf("llm client is nil")
	}
	decisions := map[int]llm.ParagraphDecision{}
	type scoreBatch struct{ items []llm.ScoringWindowInput }
	var batches []scoreBatch
	for _, items := range buildScoreBatches(windows) {
		batches = append(batches, scoreBatch{items: items})
	}

	totalBatches := len(batches)
	if onProgress != nil && totalBatches > 0 {
		onProgress("scoring", 0, totalBatches)
	}

	workers := boundedWorkerCount(len(batches), p.ScoreConcurrency, 3)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		firstErr  error
		mu        sync.Mutex
		wg        sync.WaitGroup
		sem       = make(chan struct{}, workers)
		completed int
	)

launchLoop:
	for _, batch := range batches {
		select {
		case <-ctx.Done():
			break launchLoop
		default:
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(batch scoreBatch) {
			defer wg.Done()
			defer func() { <-sem }()

			d, err := p.LLM.ScoreParagraphs(ctx, preferences, batch.items)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				return
			}

			mu.Lock()
			for _, x := range d {
				decisions[x.ParagraphIndex] = x
			}
			completed++
			c := completed
			mu.Unlock()

			if onProgress != nil && totalBatches > 0 {
				onProgress("scoring", c, totalBatches)
			}
		}(batch)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return decisions, nil
}

func boundedWorkerCount(total, configured, fallback int) int {
	n := configured
	if n <= 0 {
		n = fallback
	}
	if n < 1 {
		n = 1
	}
	if total > 0 && n > total {
		n = total
	}
	return n
}

func buildScoreBatches(windows []ScoringWindow) [][]llm.ScoringWindowInput {
	var (
		batches      [][]llm.ScoringWindowInput
		cur          []llm.ScoringWindowInput
		curCharCount int
		curTargets   int
	)

	flush := func() {
		if len(cur) == 0 {
			return
		}
		batches = append(batches, cur)
		cur = nil
		curCharCount = 0
		curTargets = 0
	}

	for _, window := range windows {
		item := llm.ScoringWindowInput{
			ContextBefore: window.ContextBefore,
			ContextAfter:  window.ContextAfter,
		}
		itemChars := len(item.ContextBefore) + len(item.ContextAfter)
		for _, target := range window.Targets {
			item.Targets = append(item.Targets, llm.ParagraphInput{
				Index: target.Index,
				Text:  target.Text,
			})
			itemChars += len(target.Text)
		}
		itemTargets := len(item.Targets)
		if len(cur) > 0 && (curTargets+itemTargets > maxBatchTargets || curCharCount+itemChars > maxBatchChars) {
			flush()
		}
		cur = append(cur, item)
		curCharCount += itemChars
		curTargets += itemTargets
	}
	flush()
	return batches
}

