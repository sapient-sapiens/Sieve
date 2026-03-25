"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { createJob, fetchJob, jobStreamUrl, resolveYoutubeTranscript } from "@/lib/api";
import type { DigestDocument, ProgressPhase } from "@/types/digest";
import { DigestProgress, type ProgressSubmitPhase } from "@/components/DigestProgress";
import { DigestReader } from "@/components/DigestReader";

type SourceMode = "raw text" | "youtube";

/** Word-for-word focus instructions used when the bundled example digest was generated. */
const EXAMPLE_FOCUS_NOTE =
  "Keep tech and tech-adjacent discussion. Personal anecdotes are fine only when they clearly connect back to technology, product decisions, company building, AI, social platforms, devices, science, or the future of computing. Omit combat sports, training/injury talk, sponsor reads, and unrelated personal tangents. When in doubt, prefer content that is at least somewhat related to tech.";

function wordCountApprox(s: string): number {
  const t = s.trim();
  if (!t) return 0;
  return t.split(/\s+/).filter(Boolean).length;
}

export default function Home() {
  const [preferences, setPreferences] = useState("");
  const [source, setSource] = useState("");
  const [sourceMode, setSourceMode] = useState<SourceMode>("raw text");
  const [youtubeUrl, setYoutubeUrl] = useState("");
  const [jobId, setJobId] = useState<string | null>(null);
  const [doc, setDoc] = useState<DigestDocument | null>(null);
  const [transcriptError, setTranscriptError] = useState<string | null>(null);
  const [jobError, setJobError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [submitPhase, setSubmitPhase] = useState<ProgressSubmitPhase>("idle");
  const [resolvedWordCount, setResolvedWordCount] = useState<number | undefined>(undefined);
  const runAbortRef = useRef<AbortController | null>(null);
  const lastSubmitRef = useRef<{ source_text: string; preferences_text?: string } | null>(null);
  const jobStreamClosedRef = useRef(false);
  const progressClockStartRef = useRef<number>(0);

  const [showExample, setShowExample] = useState(true);
  const [exampleDoc, setExampleDoc] = useState<DigestDocument | null>(null);

  useEffect(() => {
    if (!showExample) return;
    let cancelled = false;
    fetch("/example-digest.json")
      .then((r) => r.json())
      .then((data: DigestDocument) => {
        if (!cancelled) setExampleDoc(data);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [showExample]);

  const estimatedWords = useMemo(() => {
    if (sourceMode === "raw text") {
          const t = source.trim();
      if (!t) return undefined;
      return t.split(/\s+/).filter(Boolean).length;
    }
    return resolvedWordCount;
  }, [sourceMode, source, resolvedWordCount]);

  useEffect(() => {
    if (!jobId) return;
    let cancelled = false;
    let es: EventSource | null = null;
    let pollTimer: ReturnType<typeof setTimeout> | undefined;
    jobStreamClosedRef.current = false;

    const stopPoll = () => {
      if (pollTimer) clearTimeout(pollTimer);
      pollTimer = undefined;
    };

    const poll = async () => {
      try {
        const d = await fetchJob(jobId, { signal: runAbortRef.current?.signal });
        if (cancelled) return;
        if (d.progress_step != null && d.progress_step >= 1 && progressClockStartRef.current === 0) {
          progressClockStartRef.current = Date.now();
        }
        const nextDoc =
          progressClockStartRef.current > 0
            ? { ...d, progress_elapsed_ms: Date.now() - progressClockStartRef.current }
            : d;
        setDoc(nextDoc);
        if (d.status === "completed") {
          setLoading(false);
          setSubmitPhase("idle");
          stopPoll();
          return;
        }
        if (d.status === "failed") {
          setLoading(false);
          setSubmitPhase("idle");
          stopPoll();
          return;
        }
        if (!cancelled) {
          const interval = d.status === "running" ? 1000 : 3000;
          pollTimer = setTimeout(poll, interval);
        }
      } catch (e) {
        if (cancelled) return;
        if (e instanceof DOMException && e.name === "AbortError") return;
        if (e instanceof Error && e.name === "AbortError") return;
        setLoading(false);
        setSubmitPhase("idle");
        setJobError(e instanceof Error ? e.message : "Poll failed");
      }
    };

    const startFallbackPoll = () => {
      if (cancelled || jobStreamClosedRef.current) return;
      void poll();
    };

    try {
      es = new EventSource(jobStreamUrl(jobId));

      es.addEventListener("progress", (ev) => {
        if (cancelled) return;
        const data = JSON.parse((ev as MessageEvent).data) as {
          phase: ProgressPhase;
          step: number;
          total: number;
          elapsed_ms?: number;
        };
        if (data.step >= 1 && progressClockStartRef.current === 0) {
          progressClockStartRef.current = Date.now() - (data.elapsed_ms || 0);
        }
        const elapsed =
          progressClockStartRef.current > 0 ? Date.now() - progressClockStartRef.current : undefined;
        setDoc((prev) =>
          prev
            ? {
                ...prev,
                progress_phase: data.phase,
                progress_step: data.step,
                progress_total: data.total,
                progress_elapsed_ms: elapsed,
                status: "running",
              }
            : prev,
        );
      });

      es.addEventListener("done", (ev) => {
        if (cancelled) return;
        jobStreamClosedRef.current = true;
        const data = JSON.parse((ev as MessageEvent).data) as DigestDocument;
        setDoc(data);
        setLoading(false);
        setSubmitPhase("idle");
        es?.close();
      });

      es.addEventListener("failure", (ev) => {
        if (cancelled) return;
        jobStreamClosedRef.current = true;
        const data = JSON.parse((ev as MessageEvent).data) as { message: string };
        setDoc((prev) =>
          prev
            ? { ...prev, status: "failed", error: data.message }
            : {
                job_id: jobId,
                status: "failed",
                source_text: "",
                spans: [],
                omitted_ranges: [],
                word_count_before: 0,
                word_count_after: 0,
                error: data.message,
              },
        );
        setLoading(false);
        setSubmitPhase("idle");
        es?.close();
      });

      es.onerror = () => {
        if (jobStreamClosedRef.current || cancelled) return;
        es?.close();
        es = null;
        startFallbackPoll();
      };
    } catch {
      startFallbackPoll();
    }

    return () => {
      cancelled = true;
      stopPoll();
      jobStreamClosedRef.current = true;
      es?.close();
    };
  }, [jobId]);

  function cancelRun() {
    runAbortRef.current?.abort();
    runAbortRef.current = null;
    jobStreamClosedRef.current = true;
    setJobId(null);
    setLoading(false);
    setSubmitPhase("idle");
    setDoc(null);
  }

  async function runDigestJob(sourceText: string) {
    runAbortRef.current?.abort();
    runAbortRef.current = new AbortController();
    const signal = runAbortRef.current.signal;

    setShowExample(false);
    setExampleDoc(null);
    setTranscriptError(null);
    setJobError(null);
    setLoading(true);
    setSubmitPhase(sourceMode === "youtube" ? "transcript" : "job");
    setJobId(null);
    setResolvedWordCount(undefined);
    setDoc(null);
    jobStreamClosedRef.current = false;
    progressClockStartRef.current = 0;

    let text = sourceText;
    if (sourceMode === "youtube") {
      const url = youtubeUrl.trim();
      if (!url) {
        setLoading(false);
        setSubmitPhase("idle");
        setTranscriptError("YouTube URL required");
        return;
      }
      try {
        const resolved = await resolveYoutubeTranscript(url, { signal });
        text = resolved.source_text;
        setResolvedWordCount(resolved.word_count);
      } catch (e) {
        if (e instanceof DOMException && e.name === "AbortError") return;
        if (e instanceof Error && e.name === "AbortError") return;
        setLoading(false);
        setSubmitPhase("idle");
        setTranscriptError(e instanceof Error ? e.message : "Failed to fetch YouTube transcript");
        return;
      }
    } else if (!source.trim()) {
      setLoading(false);
      setSubmitPhase("idle");
      setJobError("Source text required");
      return;
    }

    const body: Parameters<typeof createJob>[0] = {
      source_text: text,
      speaker_split: "auto",
    };
    if (preferences.trim()) body.preferences_text = preferences.trim();
    lastSubmitRef.current = {
      source_text: text,
      ...(preferences.trim() ? { preferences_text: preferences.trim() } : {}),
    };

    try {
      setSubmitPhase("job");
      const { job_id } = await createJob(body, { signal });
      setJobId(job_id);
      setSubmitPhase("polling");
      setDoc({
        job_id,
        status: "running",
        source_text: text,
        spans: [],
        omitted_ranges: [],
        word_count_before: wordCountApprox(text),
        word_count_after: 0,
      });
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") return;
      if (e instanceof Error && e.name === "AbortError") return;
      setLoading(false);
      setSubmitPhase("idle");
      setJobError(e instanceof Error ? e.message : "Request failed");
    }
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    await runDigestJob(source);
  }

  async function retryJob() {
    const last = lastSubmitRef.current;
    if (last) {
      await runDigestJob(last.source_text);
    } else {
      await runDigestJob(source);
    }
  }

  const runningLabel =
    submitPhase === "transcript" ? "Fetching transcript…" : submitPhase === "job" ? "Submitting…" : "Running…";

  const showDigestReader =
    doc &&
    doc.spans &&
    doc.spans.length > 0 &&
    doc.status === "completed";

  return (
    <div className="mx-auto min-h-screen max-w-7xl px-4 py-10 sm:px-6">
      <header className="mb-10 space-y-3 text-center">
        <p className="text-xs font-semibold uppercase tracking-widest text-amber-600 dark:text-amber-400">
          Read only what matters
        </p>
        <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
          Sieve
        </h1>
        <p className="mx-auto max-w-2xl text-sm text-neutral-500 dark:text-neutral-400">
          Add raw text or a YouTube link. Say what you care about and the rest gets filtered out.
        </p>
      </header>

      <form onSubmit={onSubmit} className="mb-10 space-y-10">
        <section aria-labelledby="prefs-heading" className="digest-prefs-rail max-w-3xl">
          <h2
            id="prefs-heading"
            className="mb-4 text-[11px] font-semibold uppercase tracking-[0.12em] text-neutral-500 dark:text-neutral-400"
          >
            Your focus
          </h2>
          <p className="mb-5 max-w-xl text-sm leading-relaxed text-neutral-600 dark:text-neutral-500">
            What do you want more of? What should be skipped?
          </p>
          <div className="space-y-6">
            <fieldset className="space-y-2 text-sm">
              <legend className="text-[13px] font-medium text-[var(--foreground)]">Input</legend>
              <div className="flex flex-wrap gap-6">
                <label className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="source-mode"
                    value="raw text"
                    checked={sourceMode === "raw text"}
                    onChange={() => setSourceMode("raw text")}
                  />
                  Raw text
                </label>
                <label className="flex items-center gap-2">
                  <input
                    type="radio"
                    name="source-mode"
                    value="youtube"
                    checked={sourceMode === "youtube"}
                    onChange={() => setSourceMode("youtube")}
                  />
                  YouTube URL
                </label>
              </div>
            </fieldset>

            <label className="block space-y-1 text-sm">
              <span className="font-medium text-neutral-800 dark:text-neutral-200">Focus note</span>
              <textarea
                className="min-h-[120px] w-full rounded-lg border border-neutral-200/90 bg-[var(--background)] px-3 py-2 dark:border-neutral-700/80"
                value={preferences}
                onChange={(e) => setPreferences(e.target.value)}
                placeholder="e.g. Keep tech discussion and product strategy. Skip sponsor reads and unrelated tangents."
              />
            </label>
          </div>
        </section>

        <section aria-labelledby="source-heading" className="digest-composer p-6 sm:p-7">
          <div className="mb-6 border-b border-[var(--digest-border)] pb-5">
            <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-emerald-600/95 dark:text-emerald-400/90">
              Source
            </p>
            <h2 id="source-heading" className="mt-1 text-lg font-semibold tracking-tight text-[var(--foreground)]">
              {sourceMode === "raw text" ? "Raw text" : "Link a video"}
            </h2>
            <p className="mt-1.5 max-w-2xl text-sm text-neutral-600 dark:text-neutral-400">
              {sourceMode === "raw text"
                ? "Paste any long-form text. Blank lines between paragraphs help."
                : "Paste a link and captions will be pulled automatically."}
            </p>
          </div>
          <div className="space-y-6">
            <div className="space-y-1">
              {sourceMode === "raw text" ? (
                <textarea
                  required={sourceMode === "raw text"}
                  aria-labelledby="source-heading"
                  className="min-h-[220px] w-full rounded-lg border border-[var(--digest-border)] bg-[var(--background)] px-3 py-2 font-mono text-sm shadow-inner shadow-neutral-900/[0.02] dark:shadow-black/20"
                  value={source}
                  onChange={(e) => setSource(e.target.value)}
                  placeholder="Article, newsletter, or full transcript..."
                />
              ) : (
                <>
                  <input
                    required={sourceMode === "youtube"}
                    type="url"
                    aria-labelledby="source-heading"
                    className="w-full rounded-lg border border-[var(--digest-border)] bg-[var(--background)] px-3 py-2 shadow-inner shadow-neutral-900/[0.02] dark:shadow-black/20"
                    value={youtubeUrl}
                    onChange={(e) => setYoutubeUrl(e.target.value)}
                    placeholder="https://www.youtube.com/watch?v=..."
                  />
                  <p className="text-xs text-neutral-500 dark:text-neutral-400">
                    The video needs captions or auto-generated subtitles.
                  </p>
                </>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-3">
              {loading ? (
                <>
                  <span className="text-sm text-neutral-600 dark:text-neutral-400" aria-live="polite">
                    {runningLabel}
                  </span>
                  <button
                    type="button"
                    onClick={cancelRun}
                    className="rounded-lg border border-neutral-300 bg-[var(--background)] px-4 py-2 text-sm font-semibold text-[var(--foreground)] hover:bg-neutral-100 dark:border-neutral-600 dark:hover:bg-neutral-800"
                  >
                    Cancel
                  </button>
                </>
              ) : (
                <button
                  type="submit"
                  className="rounded-lg bg-amber-600 px-4 py-2 text-sm font-semibold text-white hover:bg-amber-700"
                >
                  Sieve it
                </button>
              )}
            </div>
          </div>
        </section>
      </form>

      <DigestProgress
        visible={loading}
        submitPhase={submitPhase}
        sourceMode={sourceMode}
        jobStatus={doc?.status ?? null}
        wordCountBefore={doc?.word_count_before}
        estimatedWords={estimatedWords}
        progressPhase={doc?.progress_phase}
        progressStep={doc?.progress_step}
        progressTotal={doc?.progress_total}
        progressElapsedMs={doc?.progress_elapsed_ms}
      />

      {transcriptError && (
        <div className="mb-6 rounded-lg border border-red-300 bg-red-50 p-4 text-sm text-red-900 dark:border-red-900 dark:bg-red-950 dark:text-red-100">
          <p>Transcript error: {transcriptError}</p>
          <button
            type="button"
            onClick={() => {
              setTranscriptError(null);
              void runDigestJob(source);
            }}
            className="mt-3 rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm font-medium text-red-900 hover:bg-red-50 dark:border-red-800 dark:bg-red-950 dark:text-red-100 dark:hover:bg-red-900"
          >
            Retry
          </button>
        </div>
      )}

      {jobError && (
        <div className="mb-6 rounded-lg border border-red-300 bg-red-50 p-4 text-sm text-red-900 dark:border-red-900 dark:bg-red-950 dark:text-red-100">
          <p>Digest error: {jobError}</p>
          {lastSubmitRef.current && (
            <button
              type="button"
              onClick={() => {
                setJobError(null);
                void retryJob();
              }}
              className="mt-3 rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm font-medium text-red-900 hover:bg-red-50 dark:border-red-800 dark:bg-red-950 dark:text-red-100 dark:hover:bg-red-900"
            >
              Retry
            </button>
          )}
        </div>
      )}

      {showExample && exampleDoc && !loading && !doc && (
        <section className="space-y-4">
          <div className="flex flex-wrap items-center gap-3">
            <div className="rounded-md bg-amber-100 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-widest text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
              Example
            </div>
            <p className="text-sm text-neutral-500 dark:text-neutral-400">
              JRE #2219, Joe Rogan & Mark Zuckerberg
            </p>
          </div>
          <div className="max-w-3xl rounded-lg border border-neutral-200/90 bg-neutral-50/90 px-4 py-3 dark:border-neutral-700/80 dark:bg-neutral-900/50">
            <p className="mb-2 text-[11px] font-semibold uppercase tracking-[0.12em] text-neutral-500 dark:text-neutral-400">
              Focus for this example
            </p>
            <p className="text-sm leading-relaxed text-neutral-700 dark:text-neutral-300">{EXAMPLE_FOCUS_NOTE}</p>
          </div>
          <DigestReader doc={exampleDoc} />
        </section>
      )}

      {doc && (
        <section className="space-y-4">
          {doc.status !== "completed" && doc.status !== "running" && (
            <div className="flex flex-wrap gap-4 text-xs text-neutral-500 dark:text-neutral-400">
              <span>
                Status: <strong className="text-[var(--foreground)]">{doc.status}</strong>
              </span>
            </div>
          )}
          {doc.error && doc.status === "failed" && (
            <div className="flex flex-wrap items-center gap-3 text-xs text-red-600 dark:text-red-400">
              <span>Error: {doc.error}</span>
              {lastSubmitRef.current && (
                <button
                  type="button"
                  onClick={() => {
                    setJobError(null);
                    void retryJob();
                  }}
                  className="rounded-md border border-red-300 bg-white px-2.5 py-1 text-[11px] font-semibold text-red-900 hover:bg-red-50 dark:border-red-800 dark:bg-red-950 dark:text-red-100 dark:hover:bg-red-900"
                >
                  Retry
                </button>
              )}
            </div>
          )}
          {showDigestReader && doc && <DigestReader doc={doc} />}
        </section>
      )}
    </div>
  );
}
