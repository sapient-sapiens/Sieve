type ApiSpanType =
  | "source_kept"
  | "truncation_marker";

// The API only returns ApiSpanType values; DigestReader materializes
// source_omitted rows locally when it expands truncation markers.
type SpanType = ApiSpanType | "source_omitted";

export type Span = {
  id: string;
  type: SpanType;
  text: string;
  start_offset?: number;
  end_offset?: number;
  reason?: string;
};

export type OmittedRange = {
  start: number;
  end: number;
  reason: string;
  word_count?: number;
};

export type ProgressPhase = "scoring" | "assembling";

export type DigestDocument = {
  job_id: string;
  status: "pending" | "running" | "completed" | "failed";
  source_text: string;
  word_count_before: number;
  word_count_after: number;
  /** blank_line | speaker_line */
  chunking_mode?: string;
  error?: string;
  spans: Span[];
  omitted_ranges: OmittedRange[];
  /** Server-reported pipeline progress while pending/running */
  progress_phase?: ProgressPhase;
  progress_step?: number;
  progress_total?: number;
  /** Client-computed elapsed time used for ETA; not part of the persisted server contract. */
  progress_elapsed_ms?: number;
};
