"use client";

import { useMemo, useState } from "react";
import type { DigestDocument, OmittedRange, Span } from "@/types/digest";

type Props = {
  doc: DigestDocument;
};

const COLLAPSE_CHARS = 400;

function resolveMarkers(
  spans: Span[],
  sourceText: string,
  omittedRanges: OmittedRange[],
): Span[] {
  const coveredStarts = new Set(
    spans
      .filter((s) => s.type === "source_omitted" && s.start_offset != null)
      .map((s) => s.start_offset!),
  );
  const uncovered = (omittedRanges ?? []).filter((r) => !coveredStarts.has(r.start));
  let ui = 0;

  return spans.map((s): Span => {
    if (s.type !== "truncation_marker") return s;
    if (s.start_offset != null && s.end_offset != null) {
      const t = sourceText.slice(s.start_offset, s.end_offset);
      if (t.trim() && t !== s.text)
        return { ...s, type: "source_omitted", text: t, reason: s.reason || s.text };
    }
    if (ui < uncovered.length) {
      const r = uncovered[ui++];
      const t = sourceText.slice(r.start, r.end);
      if (t.trim())
        return { ...s, type: "source_omitted", text: t, reason: r.reason || s.text };
    }
    return { ...s, type: "source_omitted", reason: s.reason || s.text };
  });
}

export function DigestReader({ doc }: Props) {
  const resolved = useMemo(
    () => resolveMarkers(doc.spans, doc.source_text, doc.omitted_ranges),
    [doc.spans, doc.source_text, doc.omitted_ranges],
  );

  const pct =
    doc.word_count_before > 0
      ? Math.round(((doc.word_count_before - doc.word_count_after) / doc.word_count_before) * 100)
      : 0;

  return (
    <div className="space-y-4">
      <div className="overflow-x-auto">
        <div className="min-w-[640px]">
          {/* Word-count bar aligned to the two-column grid */}
          <div className="word-count-bar">
            <div className="word-count-left">
              <span className="word-count-number word-count-number--before">
                {doc.word_count_before.toLocaleString()}
              </span>
              <span className="word-count-unit">words</span>
            </div>
            <div className="word-count-arrow">
              <svg width="36" height="14" viewBox="0 0 36 14" fill="none" aria-hidden="true" className="word-count-arrow-svg">
                <line x1="0" y1="7" x2="28" y2="7" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                <polyline points="24,2 30,7 24,12" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
              {pct > 0 && (
                <span className="word-count-pct">{pct}% shorter</span>
              )}
            </div>
            <div className="word-count-right">
              <span className="word-count-number word-count-number--after">
                {doc.word_count_after.toLocaleString()}
              </span>
              <span className="word-count-unit">words</span>
            </div>
          </div>

          <div className="diff-header">
            <span className="diff-label">Original</span>
            <span className="diff-label">Digest</span>
          </div>
          <div className="diff-grid">
            {resolved.map((sp) => (
              <DiffRow key={sp.id} span={sp} />
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function DiffRow({ span }: { span: Span }) {
  const [expanded, setExpanded] = useState(false);

  switch (span.type) {
    case "source_kept": {
      return (
        <>
          <div className="diff-col">
            <span className="whitespace-pre-wrap">{span.text}</span>
          </div>
          <div className="diff-col">
            <span className="mark-teal whitespace-pre-wrap">{span.text}</span>
          </div>
        </>
      );
    }

    case "source_omitted": {
      if (!span.text?.trim()) return null;
      const long = span.text.length > COLLAPSE_CHARS;
      return (
        <>
          <div className="diff-col">
            <div className={long && !expanded ? "diff-clamp" : ""}>
              <span className="mark-rose whitespace-pre-wrap">{span.text}</span>
            </div>
            {long && (
              <button
                type="button"
                className="mt-0.5 text-[11px] text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-300"
                onClick={() => setExpanded((v) => !v)}
              >
                {expanded ? "▴ less" : "▾ more"}
              </button>
            )}
          </div>
          <div className="diff-col">
            <span className="diff-note">{span.reason || "Omitted"}</span>
          </div>
        </>
      );
    }

    default:
      return null;
  }
}
