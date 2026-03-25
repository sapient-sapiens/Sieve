package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"interest-digest/internal/digest"
	"interest-digest/internal/llm"
	"interest-digest/internal/store"
)

const (
	maxConcurrentJobs = 4
	jobTimeout        = 10 * time.Minute
	maxRequestBody    = 2 * 1024 * 1024 // 2 MB
)

type server struct {
	st     *store.Store
	llm    *llm.Client
	pipe   *digest.Pipeline
	jobSem chan struct{}

	streamMu      sync.Mutex
	jobSubscribers map[string][]chan jobStreamEvent
}

type jobStreamEvent struct {
	Event string // progress | done | failure
	Data  json.RawMessage
}

func replayElapsedMS(createdAt string) (int64, bool) {
	if strings.TrimSpace(createdAt) == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return 0, false
	}
	elapsed := time.Since(t)
	if elapsed < 0 {
		return 0, false
	}
	return elapsed.Milliseconds(), true
}

func main() {
	loadEnvFiles(".env", "../.env")

	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "./interest-digest.db"
	}
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer st.Close()

	var lc *llm.Client
	if c, err := llm.NewFromEnv(); err == nil {
		lc = c
		log.Println("OPENAI_API_KEY loaded; LLM scoring enabled")
	} else {
		log.Printf("warning: %v — job processing will fail until key is set", err)
	}

	s := &server{
		st:             st,
		llm:            lc,
		pipe:           &digest.Pipeline{LLM: lc, ScoreConcurrency: 3},
		jobSem:         make(chan struct{}, maxConcurrentJobs),
		jobSubscribers: make(map[string][]chan jobStreamEvent),
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(120 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowOriginFunc: func(_ *http.Request, origin string) bool {
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			host := strings.ToLower(u.Hostname())
			return host == "localhost" || host == "127.0.0.1"
		},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/jobs", s.handleCreateJob)
		r.Get("/jobs/{id}", s.handleGetJob)
		r.Get("/jobs/{id}/stream", s.handleJobStream)
	})

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		if strings.HasPrefix(p, ":") {
			addr = p
		} else {
			addr = ":" + p
		}
	}
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}

type createJobReq struct {
	SourceText      string  `json:"source_text"`
	PreferencesText *string `json:"preferences_text"`
	SpeakerSplit    string  `json:"speaker_split"` // auto | on | off
}

func (s *server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var req createJobReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SourceText) == "" {
		http.Error(w, "source_text required", http.StatusBadRequest)
		return
	}
	ss := strings.TrimSpace(req.SpeakerSplit)
	if ss == "" {
		ss = "auto"
	}
	if !validSpeakerSplit(ss) {
		http.Error(w, "speaker_split must be one of: auto, on, off", http.StatusBadRequest)
		return
	}
	id, err := s.st.CreateJob(r.Context(), req.SourceText, req.PreferencesText, ss)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go s.runJob(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"job_id": id, "status": "pending"})
}

func validSpeakerSplit(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case digest.SpeakerSplitAuto, digest.SpeakerSplitOn, digest.SpeakerSplitOff:
		return true
	default:
		return false
	}
}

func loadEnvFiles(paths ...string) {
	for _, path := range paths {
		loadEnvFile(path)
	}
}

func loadEnvFile(path string) {
	path = filepath.Clean(path)
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		_ = os.Setenv(key, val)
	}
}

func (s *server) subscribeJobStream(jobID string) chan jobStreamEvent {
	ch := make(chan jobStreamEvent, 64)
	s.streamMu.Lock()
	s.jobSubscribers[jobID] = append(s.jobSubscribers[jobID], ch)
	s.streamMu.Unlock()
	return ch
}

func (s *server) unsubscribeJobStream(jobID string, ch chan jobStreamEvent) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	subs := s.jobSubscribers[jobID]
	out := subs[:0]
	for _, c := range subs {
		if c != ch {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		delete(s.jobSubscribers, jobID)
	} else {
		s.jobSubscribers[jobID] = out
	}
	close(ch)
}

func (s *server) emitJobStream(jobID, event string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev := jobStreamEvent{Event: event, Data: b}
	s.streamMu.Lock()
	subs := append([]chan jobStreamEvent(nil), s.jobSubscribers[jobID]...)
	s.streamMu.Unlock()
	for _, ch := range subs {
		func() {
			defer func() { recover() }()
			select {
			case ch <- ev:
			default:
			}
		}()
	}
}

func (s *server) handleJobStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, err := s.st.GetJob(r.Context(), id)
	if err != nil || j == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ch := s.subscribeJobStream(id)
	defer s.unsubscribeJobStream(id, ch)

	send := func(event string, payload any) {
		b, err := json.Marshal(payload)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	if j.Status == "pending" || j.Status == "running" {
		if j.ProgressTotal > 0 {
			payload := map[string]any{
				"phase": j.ProgressPhase,
				"step":  j.ProgressStep,
				"total": j.ProgressTotal,
			}
			if elapsedMS, ok := replayElapsedMS(j.CreatedAt); ok {
				payload["elapsed_ms"] = elapsedMS
			}
			send("progress", payload)
		}
	}

	// Replay current state for late subscribers
	if j.Status == "completed" && j.ResultJSON.Valid {
		send("done", json.RawMessage(j.ResultJSON.String))
		return
	}
	if j.Status == "failed" && j.Error.Valid {
		send("failure", map[string]string{"message": j.Error.String})
		return
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
			flusher.Flush()
			if ev.Event == "done" || ev.Event == "failure" {
				return
			}
		}
	}
}

func (s *server) runJob(jobID string) {
	s.jobSem <- struct{}{}
	defer func() { <-s.jobSem }()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("job %s: panic recovered: %v", jobID, r)
			_ = s.st.SetJobFailed(context.Background(), jobID, "internal error")
			s.emitJobStream(jobID, "failure", map[string]string{"message": "internal error"})
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()

	if err := s.st.SetJobRunning(ctx, jobID); err != nil {
		log.Printf("job %s: set running: %v", jobID, err)
		return
	}
	j, err := s.st.GetJob(ctx, jobID)
	if err != nil {
		log.Printf("job %s: load: %v", jobID, err)
		return
	}
	if s.llm == nil {
		_ = s.st.SetJobFailed(ctx, jobID, "OPENAI_API_KEY not configured")
		s.emitJobStream(jobID, "failure", map[string]string{"message": "OPENAI_API_KEY not configured"})
		return
	}
	preferences := resolvePrefs(j)
	ss := strings.TrimSpace(j.SpeakerSplit)
	if ss == "" {
		ss = "auto"
	}

	scoringStart := time.Now()
	onProgress := func(phase string, step, total int) {
		_ = s.st.SetJobProgress(ctx, jobID, phase, step, total)
		s.emitJobStream(jobID, "progress", map[string]any{
			"phase": phase, "step": step, "total": total,
			"elapsed_ms": time.Since(scoringStart).Milliseconds(),
		})
	}

	doc, err := s.pipe.Run(ctx, jobID, j.SourceText, preferences, ss, onProgress)
	if err != nil {
		_ = s.st.SetJobFailed(ctx, jobID, err.Error())
		s.emitJobStream(jobID, "failure", map[string]string{"message": err.Error()})
		return
	}
	if err := s.st.SetJobResult(ctx, jobID, doc); err != nil {
		log.Printf("job %s: save: %v", jobID, err)
		failCtx, failCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer failCancel()
		_ = s.st.SetJobFailed(failCtx, jobID, "failed to save digest result")
		s.emitJobStream(jobID, "failure", map[string]string{"message": "failed to save digest result"})
		return
	}
	b, _ := json.Marshal(doc)
	s.emitJobStream(jobID, "done", json.RawMessage(b))
}

func resolvePrefs(j *store.JobRow) string {
	if j.PreferencesOverride.Valid && strings.TrimSpace(j.PreferencesOverride.String) != "" {
		return strings.TrimSpace(j.PreferencesOverride.String)
	}
	return ""
}

func (s *server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, err := s.st.GetJob(r.Context(), id)
	if err != nil {
		log.Printf("handleGetJob %s: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if j == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	doc, err := s.st.DigestFromJob(j)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if doc.Status != "completed" {
		doc.WordCountBefore = digest.WordCount(j.SourceText)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}
