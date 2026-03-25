import { execFile } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { NextResponse } from "next/server";

export const runtime = "nodejs";

const PYTHON_TIMEOUT_MS = 45_000;
const PYTHON_MAX_BUFFER = 10 * 1024 * 1024;

type TranscriptResolveResponse = {
  video_id: string;
  source_text: string;
  cue_count: number;
  word_count: number;
  languages: string[];
};

function getErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Unexpected transcript resolver failure";
}

function presentableResolverError(message: string): string {
  const lowered = message.toLowerCase();
  if (
    lowered.includes("blocking requests from your ip") ||
    lowered.includes("ip has been blocked by youtube") ||
    lowered.includes("ipblocked") ||
    lowered.includes("requestblocked")
  ) {
    return "YouTube is blocking transcript requests from this network right now. Try another network, enter the transcript as raw text, or use a different video.";
  }
  if (
    lowered.includes("subtitles are disabled") ||
    lowered.includes("transcript is disabled") ||
    lowered.includes("no transcripts were found")
  ) {
    return "This video does not expose captions that Sieve can read automatically.";
  }
  return message;
}

function parseUrl(body: unknown): string {
  if (!body || typeof body !== "object") {
    throw new Error("Request body must include a YouTube url string");
  }
  const { url } = body as Record<string, unknown>;
  if (typeof url !== "string") {
    throw new Error("Request body must include a YouTube url string");
  }
  const trimmed = url.trim();
  if (!trimmed) {
    throw new Error("YouTube url is required");
  }
  return trimmed;
}

function helperScriptPath(): string {
  const candidates = [
    path.resolve(process.cwd(), "clean_youtube_transcript.py"),
    path.resolve(process.cwd(), "..", "clean_youtube_transcript.py"),
  ];
  const script = candidates.find((candidate) => fs.existsSync(candidate));
  if (!script) {
    throw new Error("Could not locate clean_youtube_transcript.py");
  }
  return script;
}

function runTranscriptResolver(url: string): Promise<TranscriptResolveResponse> {
  const script = helperScriptPath();
  return new Promise((resolve, reject) => {
    execFile(
      "python3",
      [script, "--youtube-url", url, "--json"],
      {
        timeout: PYTHON_TIMEOUT_MS,
        maxBuffer: PYTHON_MAX_BUFFER,
      },
      (error, stdout, stderr) => {
        if (error) {
          const detail = stderr.trim() || error.message;
          reject(new Error(detail));
          return;
        }

        try {
          const payload = JSON.parse(stdout) as Partial<TranscriptResolveResponse>;
          if (
            typeof payload.source_text !== "string" ||
            typeof payload.video_id !== "string" ||
            !Array.isArray(payload.languages)
          ) {
            throw new Error("Python helper returned invalid transcript JSON");
          }
          resolve({
            source_text: payload.source_text,
            video_id: payload.video_id,
            cue_count: typeof payload.cue_count === "number" ? payload.cue_count : 0,
            word_count: typeof payload.word_count === "number" ? payload.word_count : 0,
            languages: payload.languages.filter((value): value is string => typeof value === "string"),
          });
        } catch (parseError) {
          reject(parseError instanceof Error ? parseError : new Error("Failed to parse transcript helper output"));
        }
      },
    );
  });
}

function statusForError(message: string): number {
  const lowered = message.toLowerCase();
  if (lowered.includes("url is required") || lowered.includes("could not extract a youtube video id")) {
    return 400;
  }
  if (lowered.includes("not installed") || lowered.includes("no module named") || lowered.includes("python3")) {
    return 503;
  }
  if (lowered.includes("failed to fetch transcript") || lowered.includes("empty after cleaning")) {
    return 422;
  }
  return 500;
}

export async function POST(request: Request) {
  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "Invalid JSON body" }, { status: 400 });
  }

  let url = "";
  try {
    url = parseUrl(body);
  } catch (error) {
    return NextResponse.json({ error: getErrorMessage(error) }, { status: 400 });
  }

  try {
    const resolved = await runTranscriptResolver(url);
    return NextResponse.json(resolved);
  } catch (error) {
    const rawMessage = getErrorMessage(error);
    return NextResponse.json(
      { error: presentableResolverError(rawMessage) },
      { status: statusForError(rawMessage) },
    );
  }
}
