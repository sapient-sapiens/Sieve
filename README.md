# Sieve

**Sieve** turns long text or YouTube transcripts into a shorter, preference-aware read. A **Go** API runs the pipeline (chunk → score with an LLM → assemble spans). **Next.js** shows a side-by-side **original vs digest** view with word counts and cut markers.

## Architecture

- **Backend:** `backend/cmd/server` — Chi router, SQLite (`DATABASE_PATH`, default `./interest-digest.db`), async jobs, OpenAI-compatible chat API (`OPENAI_API_KEY`, optional `OPENAI_MODEL`, `OPENAI_BASE_URL`).
- **Frontend:** `web/` — Tailwind, `EventSource` on `GET /api/v1/jobs/:id/stream` for live progress (`progress`, `done`, `failure`) with polling fallback, plus a Next.js route for YouTube transcript fetch.
- **Contract:** [docs/SPAN_SCHEMA.md](docs/SPAN_SCHEMA.md), [schemas/digest-document.schema.json](schemas/digest-document.schema.json).

## Quick start

### 1. Backend

```bash
cd backend
export OPENAI_API_KEY=sk-...
go run ./cmd/server
```

Listens on `:8080` (override with `PORT`).

### 2. Frontend

```bash
cd web
cp .env.example .env.local
npm run dev
```

Open [http://localhost:3000](http://localhost:3000). Set `NEXT_PUBLIC_GO_API_URL` if the API is not on `127.0.0.1:8080`.

### 3. Optional YouTube transcript setup

```bash
python3 -m pip install -r tools/youtube-transcript-requirements.txt
```

Flow: Next.js runs `python3 clean_youtube_transcript.py --youtube-url ... --json` → plain text → `POST /api/v1/jobs` on the Go API.

## Sample CLI run

With the API running and a real key:

```bash
export OPENAI_API_KEY=sk-...
# optional: GO_API=http://127.0.0.1:8080
./scripts/run-jre-digest.sh
```

## API (summary)

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/jobs` | Body: `source_text`, optional `preferences_text`, `speaker_split` (`auto` / `on` / `off`) |
| `GET` | `/api/v1/jobs/:id` | `DigestDocument` JSON (poll) |
| `GET` | `/api/v1/jobs/:id/stream` | SSE: `progress`, `done`, `failure` |

The web app also exposes `POST /api/youtube-transcript` (Next-only) to resolve a YouTube URL to text before calling Go.

CORS allows `http://localhost:3000` and `http://127.0.0.1:3000`.

## License

MIT (adjust as needed).
# Sieve
