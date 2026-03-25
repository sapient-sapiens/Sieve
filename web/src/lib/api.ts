import type { DigestDocument } from "@/types/digest";

export function goApiBase(): string {
  const b = process.env.NEXT_PUBLIC_GO_API_URL ?? "http://127.0.0.1:8080";
  return b.replace(/\/$/, "");
}

export async function createJob(
  body: {
    source_text: string;
    preferences_text?: string;
    /** Always "auto" from the web UI — transcript vs paragraph chunking is decided server-side. */
    speaker_split?: string;
  },
  init?: { signal?: AbortSignal },
): Promise<{ job_id: string; status: string }> {
  const res = await fetch(`${goApiBase()}/api/v1/jobs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal: init?.signal,
  });
  if (!res.ok) throw new Error(await res.text());
  return await res.json();
}

export async function resolveYoutubeTranscript(
  url: string,
  init?: { signal?: AbortSignal },
): Promise<{
  source_text: string;
  video_id: string;
  cue_count: number;
  word_count: number;
  languages: string[];
}> {
  const res = await fetch("/api/youtube-transcript", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url }),
    signal: init?.signal,
  });
  if (!res.ok) {
    let message = "Failed to resolve YouTube transcript";
    try {
      const payload = (await res.json()) as { error?: string };
      if (payload?.error) {
        message = payload.error;
      }
    } catch {
      // Fall back to the generic message above.
    }
    throw new Error(message);
  }
  return await res.json();
}

export async function fetchJob(id: string, init?: { signal?: AbortSignal }): Promise<DigestDocument> {
  const res = await fetch(`${goApiBase()}/api/v1/jobs/${id}`, { cache: "no-store", signal: init?.signal });
  if (!res.ok) throw new Error(await res.text());
  return await res.json();
}

export function jobStreamUrl(jobId: string): string {
  return `${goApiBase()}/api/v1/jobs/${jobId}/stream`;
}
