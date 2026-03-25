#!/usr/bin/env python3
"""
Strip YouTube copy-paste transcript timestamps and optionally fetch a transcript from a YouTube URL.

Typical pasted line shape:
  0:066 seconds<dialogue>
  1:021 minute, 2 seconds<dialogue>
  12:0012 minutes<dialogue>   # no space before text sometimes
  25:0025 minutes, 8 seconds<dialogue>
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from argparse import ArgumentDefaultsHelpFormatter, RawDescriptionHelpFormatter
from pathlib import Path
from typing import Iterable, Sequence


class _HelpFormatter(ArgumentDefaultsHelpFormatter, RawDescriptionHelpFormatter):
    """Show defaults and preserve line breaks in description/epilog."""

    pass


YOUTUBE_ID = re.compile(r"^[A-Za-z0-9_-]{11}$")
TURN_MARKER = re.compile(r"^>+\s*")
SFX_ONLY = re.compile(r"^\[(music|applause|laughter|cheering|cheers)\]$", re.IGNORECASE)
BRACKETED_NOISE = re.compile(
    r"\s*\[(?:laughter|laughing|clears?\s*throat|music|applause|cheering|cheers|inaudible"
    r"|crosstalk|silence|pause|sigh|sighs|cough|coughing|sniffing|snoring)\]\s*",
    re.IGNORECASE,
)
CENSORED_WORD = re.compile(r"\[\s*__\s*\]")
INTRO_BOILERPLATE = re.compile(
    r"(?i)(?:joe\s*rogan\s*(?:podcast|experience)|check\s+it\s+out|train\s+by\s+day|all\s+day)"
)

# After the clock, YouTube repeats the cue as plain text. Short videos use mm:ss; long ones use h:mm:ss.
TIMESTAMP = re.compile(
    r"(?:"
    # h:mm:ss — include "2 hoursthe", "1 hour, 13 minutesIf", "1 hour, 4 seconds", "2 hours, 1 minute, 8 seconds"
    r"\d+:\d{2}:\d{2}"
    r"(?:"
    r"\d+ hours?, (?:\d+ minutes?, )?\d+ seconds?|"
    r"\d+ hours?, \d+ minutes(?!,)|"  # "1 hour, 13 minutesIf…" (no space before dialogue)
    r"\d+ hours"  # "2 hoursthe…"
    r")|"
    # m:ss
    r"\d+:\d{2}"
    r"(?:"
    r"\d+ seconds|"
    r"\d+ minute, \d+ seconds?|"
    r"\d+ minutes, \d+ seconds?|"
    r"\d+ minutes"
    r")"
    r")"
)


def strip_timestamp(line: str) -> str | None:
    line = line.rstrip("\n")
    m = TIMESTAMP.match(line)
    if not m:
        return None
    return line[m.end() :].strip()


def normalize_whitespace(text: str) -> str:
    return re.sub(r"\s+", " ", text).strip()


def extract_dialogues(text: str, keep_header: bool = False) -> tuple[list[str], list[str]]:
    lines = text.splitlines()
    header: list[str] = []
    dialogues: list[str] = []
    seen_ts = False

    for line in lines:
        cleaned = strip_timestamp(line)
        if cleaned is None:
            if not seen_ts and keep_header:
                stripped = line.strip()
                if stripped:
                    header.append(stripped)
            continue
        seen_ts = True
        if cleaned:
            dialogues.append(cleaned)

    return header, dialogues


def format_dialogues(
    dialogues: Sequence[str],
    *,
    header: Sequence[str] | None = None,
    alternate: bool = True,
    speaker1: str = "Speaker 1",
    speaker2: str = "Speaker 2",
) -> str:
    out: list[str] = []
    if header:
        out.append("\n".join(header))
        out.append("")

    if alternate:
        labeled = [f"{speaker1 if i % 2 == 0 else speaker2}: {t}" for i, t in enumerate(dialogues)]
        body = ("\n".join(out) + "\n" if out else "") + "\n".join(labeled)
    else:
        out.extend(dialogues)
        body = "\n\n".join(out)

    body = body.strip()
    return f"{body}\n" if body else ""


def clean_pasted_transcript(
    text: str,
    *,
    speaker1: str = "Speaker 1",
    speaker2: str = "Speaker 2",
    alternate: bool = True,
    keep_header: bool = False,
) -> str:
    header, dialogues = extract_dialogues(text, keep_header=keep_header)
    return format_dialogues(
        dialogues,
        header=header,
        alternate=alternate,
        speaker1=speaker1,
        speaker2=speaker2,
    )


def extract_video_id(url_or_id: str) -> str:
    raw = url_or_id.strip()
    if YOUTUBE_ID.fullmatch(raw):
        return raw

    patterns = [
        r"(?:v=|\/shorts\/|youtu\.be\/)([A-Za-z0-9_-]{11})",
        r"youtube\.com\/embed\/([A-Za-z0-9_-]{11})",
    ]
    for pattern in patterns:
        m = re.search(pattern, raw)
        if m:
            return m.group(1)
    raise ValueError("could not extract a YouTube video ID from the provided URL")


def _load_youtube_api():
    try:
        from youtube_transcript_api import YouTubeTranscriptApi  # type: ignore
    except ImportError as exc:
        raise RuntimeError(
            "youtube-transcript-api is not installed. Run: pip install -r tools/youtube-transcript-requirements.txt"
        ) from exc
    return YouTubeTranscriptApi


def fetch_youtube_snippets(video_ref: str, languages: Sequence[str] | None = None) -> tuple[str, list[object]]:
    video_id = extract_video_id(video_ref)
    langs = list(languages or ("en", "en-US", "en-GB"))
    api_type = _load_youtube_api()

    try:
        api = api_type()
        if hasattr(api, "fetch"):
            return video_id, list(api.fetch(video_id, languages=langs))
        if hasattr(api_type, "get_transcript"):
            return video_id, list(api_type.get_transcript(video_id, languages=langs))
    except Exception as exc:
        raise RuntimeError(f"failed to fetch transcript for {video_id}: {exc}") from exc

    raise RuntimeError("youtube-transcript-api does not expose a supported fetch method")


def _snippet_text(snippet: object) -> str:
    if isinstance(snippet, dict):
        return str(snippet.get("text", ""))
    return str(getattr(snippet, "text", ""))


MUSIC_OR_SFX = re.compile(r"^[♪\[\(].*[♪\]\)]$")


def _clean_snippet_text(snippet: object) -> str:
    raw = _snippet_text(snippet)
    text = strip_timestamp(raw) or raw
    text = TURN_MARKER.sub("", text)
    text = BRACKETED_NOISE.sub(" ", text)
    text = CENSORED_WORD.sub("****", text)
    text = normalize_whitespace(text)
    return text


def _merge_into_turns(texts: list[str], target_words: int = 25) -> list[str]:
    """Merge short caption cues into substantial speaker-turn-sized chunks.

    Aims for ~target_words per turn (matching cleaned.txt style: ~20-35 words).
    Prefers to break after sentence-ending punctuation when past the soft target.
    Hard-breaks when adding the next cue would exceed target + 8 words.
    """
    hard_cap = target_words + 8
    turns: list[str] = []
    current = ""
    for text in texts:
        if not current:
            current = text
            continue
        cw = len(current.split())
        nw = len(text.split())
        ends_sentence = current.rstrip()[-1:] in ".!?\u201d"
        if cw >= target_words and ends_sentence:
            turns.append(current)
            current = text
            continue
        if cw + nw > hard_cap and cw >= 12:
            turns.append(current)
            current = text
            continue
        current = f"{current} {text}"
    if current:
        turns.append(current)
    return turns


def _is_boilerplate(text: str) -> bool:
    return bool(INTRO_BOILERPLATE.search(text)) and len(text.split()) < 40


def clean_fetched_snippets(snippets: Iterable[object]) -> str:
    cleaned: list[str] = []
    last = ""
    boilerplate_window = True
    for snippet in snippets:
        text = _clean_snippet_text(snippet)
        if not text:
            continue
        if SFX_ONLY.match(text) or MUSIC_OR_SFX.match(text):
            continue
        if text == last:
            continue
        if boilerplate_window and _is_boilerplate(text):
            continue
        boilerplate_window = False
        cleaned.append(text)
        last = text

    if not cleaned:
        return ""

    turns = _merge_into_turns(cleaned, target_words=30)
    labeled = [
        f"Speaker {1 if i % 2 == 0 else 2}: {turn}"
        for i, turn in enumerate(turns)
    ]
    body = "\n".join(labeled).strip()
    return f"{body}\n" if body else ""


def parse_languages(raw: str) -> list[str]:
    parts = [x.strip() for x in raw.split(",")]
    return [x for x in parts if x]


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        description=(
            "Strip YouTube transcript timestamps, optionally label alternating speakers, "
            "or fetch transcript text directly from a YouTube URL/video ID."
        ),
        formatter_class=_HelpFormatter,
        epilog="""
Examples:
  %(prog)s raw.txt out.txt
  %(prog)s raw.txt out.txt --speaker1 "Joe Rogan" --speaker2 "Pierre Poilievre"
  %(prog)s raw.txt out.txt --no-alternate
  %(prog)s --youtube-url "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
  %(prog)s --youtube-url "https://www.youtube.com/watch?v=dQw4w9WgXcQ" --json
""".strip(),
    )
    p.add_argument("input", nargs="?", type=Path, help="Input .txt file")
    p.add_argument("output", nargs="?", type=Path, help="Output .txt file to write")
    p.add_argument("--youtube-url", help="Fetch transcript from a YouTube URL")
    p.add_argument("--video-id", help="Fetch transcript from a YouTube video ID")
    p.add_argument(
        "--languages",
        default="en,en-US,en-GB",
        help="Comma-separated language fallbacks for transcript retrieval",
    )
    p.add_argument(
        "--json",
        action="store_true",
        help="Emit JSON to stdout in YouTube fetch mode",
    )
    p.add_argument(
        "--speaker1",
        default="Speaker 1",
        metavar="NAME",
        help="Name for the 1st, 3rd, 5th, ... cue",
    )
    p.add_argument(
        "--speaker2",
        default="Speaker 2",
        metavar="NAME",
        help="Name for the 2nd, 4th, 6th, ... cue",
    )
    p.add_argument(
        "--no-alternate",
        action="store_true",
        help="Do not add speaker labels; output one paragraph per cue separated by blank lines",
    )
    p.add_argument(
        "--keep-header",
        action="store_true",
        help="Keep lines before the first timestamp as a header block",
    )
    return p


def run_legacy_mode(args: argparse.Namespace, parser: argparse.ArgumentParser) -> int:
    if args.input is None or args.output is None:
        parser.error("legacy mode requires both input and output paths")
    text = args.input.read_text(encoding="utf-8", errors="replace")
    body = clean_pasted_transcript(
        text,
        speaker1=args.speaker1,
        speaker2=args.speaker2,
        alternate=not args.no_alternate,
        keep_header=args.keep_header,
    )
    args.output.write_text(body, encoding="utf-8")
    return 0


def run_youtube_mode(args: argparse.Namespace, parser: argparse.ArgumentParser) -> int:
    if args.input is not None or args.output is not None:
        parser.error("input/output paths cannot be combined with --youtube-url or --video-id")

    refs = [x for x in (args.youtube_url, args.video_id) if x]
    if len(refs) != 1:
        parser.error("provide exactly one of --youtube-url or --video-id")

    video_id, snippets = fetch_youtube_snippets(refs[0], parse_languages(args.languages))
    source_text = clean_fetched_snippets(snippets)
    if not source_text.strip():
        raise RuntimeError(f"transcript for {video_id} was empty after cleaning")

    if args.json:
        payload = {
            "video_id": video_id,
            "source_text": source_text,
            "cue_count": len(source_text.strip().splitlines()),
            "word_count": len(source_text.split()),
            "languages": parse_languages(args.languages),
        }
        print(json.dumps(payload, ensure_ascii=False))
    else:
        sys.stdout.write(source_text)
    return 0


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()

    try:
        if args.youtube_url or args.video_id:
            return run_youtube_mode(args, parser)
        return run_legacy_mode(args, parser)
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
