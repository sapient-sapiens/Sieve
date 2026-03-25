"use client";

import { useEffect, useState } from "react";

import type { ProgressPhase } from "@/types/digest";

export type ProgressSubmitPhase = "idle" | "transcript" | "job" | "polling";

type JobStatus = "pending" | "running" | "completed" | "failed" | null;

type Props = {
  visible: boolean;
  submitPhase: ProgressSubmitPhase;
  sourceMode: "raw text" | "youtube";
  jobStatus: JobStatus;
  wordCountBefore?: number;
  estimatedWords?: number;
  progressPhase?: ProgressPhase;
  progressStep?: number;
  progressTotal?: number;
  progressElapsedMs?: number;
};

const ACTIVITY_PHRASES = [
  "Reading between the lines…",
  "Separating signal from noise…",
  "Finding the good parts…",
  "Skimming the fluff…",
  "Picking out what matters…",
  "Weighing every paragraph…",
  "Deciding what stays…",
];

function runningBarLabel(phase: ProgressPhase | undefined): string {
  switch (phase) {
    case "scoring":
      return "Scoring";
    case "assembling":
      return "Assembling";
    default:
      return "Processing";
  }
}

function prepLine(submitPhase: ProgressSubmitPhase, sourceMode: "raw text" | "youtube", jobStatus: JobStatus): string | null {
  if (submitPhase === "transcript") return "Fetching captions…";
  if (submitPhase === "job") return "Submitting…";
  if (submitPhase === "polling" && (jobStatus == null || jobStatus === "pending")) {
    return sourceMode === "youtube" ? "Queued, starting soon…" : "Starting…";
  }
  return null;
}

function formatEta(ms: number): string {
  if (ms < 5000) return "Almost done…";
  const secs = Math.ceil(ms / 1000);
  if (secs < 60) return `~${secs}s left`;
  const mins = Math.floor(secs / 60);
  const remSecs = secs % 60;
  if (remSecs === 0) return `~${mins}m left`;
  return `~${mins}m ${remSecs}s left`;
}

function computeEta(step: number, total: number, elapsedMs: number): string | null {
  if (step <= 0 || total <= 0 || elapsedMs <= 0) return null;
  if (step >= total) return null;
  const avgPerBatch = elapsedMs / step;
  const remaining = avgPerBatch * (total - step);
  return formatEta(remaining);
}

function useRotatingPhrase(active: boolean): string {
  const [idx, setIdx] = useState(0);
  useEffect(() => {
    if (!active) return;
    setIdx(Math.floor(Math.random() * ACTIVITY_PHRASES.length));
    const id = setInterval(() => {
      setIdx((prev) => (prev + 1) % ACTIVITY_PHRASES.length);
    }, 3500);
    return () => clearInterval(id);
  }, [active]);
  return ACTIVITY_PHRASES[idx];
}

export function DigestProgress({
  visible,
  submitPhase,
  sourceMode,
  jobStatus,
  wordCountBefore,
  estimatedWords,
  progressPhase,
  progressStep,
  progressTotal,
  progressElapsedMs,
}: Props) {
  const displayWords = wordCountBefore ?? estimatedWords;
  const hasServerProgress =
    jobStatus === "running" &&
    progressTotal != null &&
    progressTotal > 0 &&
    progressStep != null;
  const runPercent =
    hasServerProgress && progressTotal != null && progressStep != null
      ? Math.min(99, Math.round((progressStep / progressTotal) * 100))
      : null;
  const eta =
    hasServerProgress && progressStep != null && progressTotal != null && progressElapsedMs != null
      ? computeEta(progressStep, progressTotal, progressElapsedMs)
      : null;
  const early = prepLine(submitPhase, sourceMode, jobStatus);

  const showRotating = hasServerProgress && progressPhase === "scoring" && !eta;
  const phrase = useRotatingPhrase(showRotating);

  if (!visible) return null;

  const barHeadingId = "digest-running-label";

  return (
    <div
      className="digest-progress mb-8 rounded-xl border border-[var(--digest-border)] bg-[var(--digest-panel)] px-4 py-4 sm:px-5"
      role="status"
      aria-live="polite"
      aria-busy={jobStatus === "running"}
    >
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <p className="text-[11px] font-semibold uppercase tracking-[0.14em] text-emerald-700/90 dark:text-emerald-400/85">
          In progress
        </p>
        {displayWords != null && displayWords > 0 && (
          <p className="text-xs tabular-nums text-neutral-500 dark:text-neutral-400">
            ~{displayWords.toLocaleString()} words in source
          </p>
        )}
      </div>

      {early && jobStatus !== "running" ? (
        <div className="mb-2 text-xs text-neutral-600 dark:text-neutral-400" id={barHeadingId}>
          <span className="font-medium text-[var(--foreground)]">{early}</span>
        </div>
      ) : null}

      {jobStatus === "running" ? (
        <div className="mb-1">
          {hasServerProgress && runPercent != null ? (
            <>
              <div
                className="mb-2 flex flex-col gap-1 text-xs text-neutral-600 dark:text-neutral-400 sm:flex-row sm:items-center sm:justify-between sm:gap-2"
                id={barHeadingId}
              >
                <span className="font-medium text-[var(--foreground)]">
                  {runningBarLabel(progressPhase)}
                  {progressTotal != null && progressStep != null && progressTotal > 0 ? (
                    <span className="font-normal text-neutral-500 dark:text-neutral-400">
                      {" "}
                      · batch {progressStep} of {progressTotal}
                    </span>
                  ) : null}
                </span>
                {eta ? (
                  <span className="tabular-nums text-emerald-600/80 dark:text-emerald-400/80" aria-hidden>
                    {eta}
                  </span>
                ) : (
                  <span className="tabular-nums text-neutral-500" aria-hidden>
                    {runPercent}%
                  </span>
                )}
              </div>
              <div
                className="h-2 w-full overflow-hidden rounded-full bg-neutral-200/80 dark:bg-neutral-700/80"
                role="progressbar"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={runPercent}
                aria-labelledby={barHeadingId}
                aria-valuetext={`${progressPhase}: batch ${progressStep} of ${progressTotal} (${runPercent} percent)${eta ? `, ${eta}` : ""}`}
              >
                <div
                  className="h-full rounded-full bg-gradient-to-r from-emerald-600 to-emerald-400 transition-[width] duration-300 ease-out motion-reduce:transition-none"
                  style={{ width: `${runPercent}%` }}
                />
              </div>
              {progressPhase === "scoring" && (
                <p className="mt-2 text-xs leading-relaxed text-neutral-500 dark:text-neutral-400 transition-opacity duration-500">
                  {phrase}
                </p>
              )}
              {progressPhase === "assembling" && (
                <p className="mt-2 text-xs leading-relaxed text-neutral-500 dark:text-neutral-400">
                  Building the final digest…
                </p>
              )}
            </>
          ) : (
            <>
              <div className="mb-2 text-xs text-neutral-600 dark:text-neutral-400" id={barHeadingId}>
                <span className="font-medium text-[var(--foreground)]">Starting…</span>
              </div>
              <div
                className="h-2 w-full overflow-hidden rounded-full bg-neutral-200/80 dark:bg-neutral-700/80"
                role="progressbar"
                aria-labelledby={barHeadingId}
                aria-valuetext="Waiting for job progress."
              >
                <div className="digest-progress-shimmer h-full w-1/3 rounded-full bg-gradient-to-r from-emerald-500/0 via-emerald-500/70 to-emerald-500/0" />
              </div>
              <p className="mt-2 text-xs leading-relaxed text-neutral-500 dark:text-neutral-400">
                Connecting to the job…
              </p>
            </>
          )}
        </div>
      ) : (
        <div
          className="mb-4 h-2 w-full overflow-hidden rounded-full bg-neutral-200/80 dark:bg-neutral-700/80"
          aria-hidden
        >
          <div className="digest-progress-shimmer h-full w-1/3 rounded-full bg-gradient-to-r from-emerald-500/0 via-emerald-500/70 to-emerald-500/0" />
        </div>
      )}
    </div>
  );
}
