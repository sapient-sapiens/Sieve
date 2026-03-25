package digest

// SpanType matches schemas/digest-document.schema.json
type SpanType string

const (
	SpanSourceKept       SpanType = "source_kept"
	SpanTruncationMarker SpanType = "truncation_marker"
)

// Span is one visual segment in reading order.
type Span struct {
	ID          string   `json:"id"`
	Type        SpanType `json:"type"`
	Text        string   `json:"text"`
	StartOffset *int     `json:"start_offset,omitempty"`
	EndOffset   *int     `json:"end_offset,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// OmittedRange is a contiguous removed region in source_text (byte offsets).
type OmittedRange struct {
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Reason    string `json:"reason"`
	WordCount int    `json:"word_count,omitempty"`
}

// DigestDocument is the API payload for a completed (or in-progress) job view.
type DigestDocument struct {
	JobID               string         `json:"job_id"`
	Status              string         `json:"status"`
	SourceText          string         `json:"source_text"`
	WordCountBefore     int            `json:"word_count_before"`
	WordCountAfter      int            `json:"word_count_after"`
	ChunkingMode        string         `json:"chunking_mode,omitempty"` // blank_line | speaker_line
	Error               string         `json:"error,omitempty"`
	Spans               []Span         `json:"spans"`
	OmittedRanges       []OmittedRange `json:"omitted_ranges"`
	// Progress is set only while status is pending/running (from DB); omitted when completed.
	ProgressPhase string `json:"progress_phase,omitempty"` // scoring | assembling
	ProgressStep  int    `json:"progress_step,omitempty"`  // completed units within phase
	ProgressTotal int    `json:"progress_total,omitempty"`   // total units within the current phase
}

func PtrInt(v int) *int           { return &v }
