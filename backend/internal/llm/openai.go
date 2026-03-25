package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
	sem     chan struct{}
}

func NewFromEnv() (*Client, error) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-5"
	}
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	maxConcurrent := envInt("OPENAI_MAX_CONCURRENCY", 4)
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		APIKey:  key,
		Model:   model,
		BaseURL: strings.TrimRight(base, "/"),
		HTTP:    &http.Client{Timeout: 240 * time.Second, Transport: tr},
		sem:     make(chan struct{}, max(1, maxConcurrent)),
	}, nil
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 {
		return fallback
	}
	return n
}

func reasoningEffortForModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(model, "gpt-5") {
		return "low"
	}
	return ""
}

func temperatureForModel(model string, v float64) *float64 {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(model, "gpt-5") {
		return nil
	}
	return &v
}

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	Temperature     *float64        `json:"temperature,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
	// Stream must be sent as false for some proxies that otherwise default to streaming,
	// which breaks JSON response parsing (SSE starts with "event:").
	Stream *bool `json:"stream,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type ParagraphInput struct {
	Index int
	Text  string
}

type ScoringWindowInput struct {
	ContextBefore string
	ContextAfter  string
	Targets       []ParagraphInput
}

// ScoreParagraphs asks the model for keep/omit per paragraph index.
func (c *Client) ScoreParagraphs(ctx context.Context, preferences string, windows []ScoringWindowInput) (decisions []ParagraphDecision, err error) {
	hasPreferences := strings.TrimSpace(preferences) != ""

	var sb strings.Builder

	if hasPreferences {
		sb.WriteString("READER PREFERENCES:\n")
		sb.WriteString(preferences)
		sb.WriteString("\n\n")
		sb.WriteString("RULES:\n")
		sb.WriteString("1. Classify each numbered target turn based PRIMARILY on that target turn's own text.\n")
		sb.WriteString("2. Context before/after is only for disambiguation. Do NOT let adjacent discussion override what the target turn itself is about.\n")
		sb.WriteString("3. Keep turns that clearly match the reader's preferred focus.\n")
		sb.WriteString("4. Omit turns that clearly fall outside that focus, including tangents, side topics, and examples the reader explicitly does not want.\n")
		sb.WriteString("5. If the preferences imply 'mostly X' or 'everything besides X', omit turns that are not primarily about X even if they are interesting on their own.\n")
		sb.WriteString("6. Personal anecdotes are kept only when they clearly tie back to the preferred focus.\n")
		sb.WriteString("7. Concrete examples of unwanted topics still count as unwanted, even when used inside broader business, product, or policy discussion.\n")
		sb.WriteString("8. Metaphorical wording alone is not a topic match.\n")
		sb.WriteString("9. Do NOT omit for repetition alone unless the preferences themselves suggest strong condensation.\n\n")
		sb.WriteString("CHECKLIST FOR EACH TARGET TURN:\n")
		sb.WriteString("- Is this turn itself aligned with what the reader wants more of?\n")
		sb.WriteString("- Is it instead a tangent or side topic the reader would likely want less of?\n")
		sb.WriteString("- Is it a personal story? If so, does that story clearly tie back to the preferred focus?\n")
		sb.WriteString("- Is it using an unwanted topic as a concrete example? If yes, omit it.\n\n")
		sb.WriteString("EXAMPLES:\n")
		sb.WriteString("Prefs: keep tech and tech-adjacent discussion; skip combat sports, sponsor reads, and unrelated personal stories | Turn: \"This episode is brought to you by Athletic Greens\" → omit (sponsor read)\n")
		sb.WriteString("Prefs: keep tech and tech-adjacent discussion; skip combat sports, sponsor reads, and unrelated personal stories | Turn: \"We need to fight for better content moderation\" → keep (tech/policy, metaphorical wording)\n")
		sb.WriteString("Prefs: keep tech and tech-adjacent discussion; skip combat sports, sponsor reads, and unrelated personal stories | Turn: \"You got into Jiu-Jitsu and now you look thicker\" → omit (non-tech training/personal tangent)\n")
		sb.WriteString("Prefs: keep tech and tech-adjacent discussion; skip combat sports, sponsor reads, and unrelated personal stories | Turn: \"If you're playing a boxing game in VR, you need force feedback on the other side\" → omit (unwanted combat-sports example)\n")
		sb.WriteString("Prefs: mostly tech; personal anecdotes are fine only if they tie back to tech | Turn: \"When we launched News Feed, the internal debate was intense\" → keep (personal anecdote tied to product history)\n")
		sb.WriteString("Prefs: mostly tech; personal anecdotes are fine only if they tie back to tech | Turn: \"My doctor told me the cadaver graft is 150% stronger\" → omit (personal health tangent)\n")
		sb.WriteString("Prefs: mostly tech; omit anything besides tech | Turn: \"AI labs will need more compute, energy, and capital over the next decade\" → keep (core focus)\n")
	} else {
		sb.WriteString("The reader wants a CONDENSED version (no topic preferences).\n")
		sb.WriteString("Aggressively omit filler, banter, repetitive exchanges, tangents, small talk.\n")
		sb.WriteString("Keep real information, arguments, facts, meaningful narrative. Target: 30-50%.\n\n")
	}

	sb.WriteString("Output: reason_code (avoid_topic|main_thread|context|fallback_keep|low_value_tangent), reason_detail (short phrase).\n")
	sb.WriteString("Only score [numbered] target turns.\n\n")

	for i, w := range windows {
		before := w.ContextBefore
		if len(before) > 1200 {
			before = "…" + before[len(before)-1200:]
		}
		after := w.ContextAfter
		if len(after) > 900 {
			after = after[:900] + "…"
		}
		fmt.Fprintf(&sb, "--- Window %d ---\n", i+1)
		if strings.TrimSpace(before) != "" {
			fmt.Fprintf(&sb, "Previously discussing:\n%s\n\n", before)
		}
		sb.WriteString("Score these turns:\n")
		for _, target := range w.Targets {
			text := target.Text
			if len(text) > 1200 {
				text = text[:1200] + "…"
			}
			fmt.Fprintf(&sb, "[%d]: %s\n\n", target.Index, text)
		}
		if strings.TrimSpace(after) != "" {
			fmt.Fprintf(&sb, "Next up:\n%s\n\n", after)
		}
	}
	sb.WriteString(`Return JSON only:
{"decisions":[{"paragraph_index":0,"action":"keep","reason_code":"main_thread","reason_detail":"core election discussion"}]}
One decision per [numbered] turn.`)

	msgs := []chatMessage{
		{Role: "user", Content: sb.String()},
	}
	if hasPreferences {
		msgs = []chatMessage{
			{Role: "system", Content: "You are a strict local preference filter. For each numbered target turn, decide keep or omit from the target turn itself using the reader's single preference instruction. Keep what matches the reader's focus; omit what falls outside it."},
			{Role: "user", Content: sb.String()},
		}
	}
	body := chatRequest{
		Model:           c.Model,
		Messages:        msgs,
		Temperature:     temperatureForModel(c.Model, 0.2),
		ReasoningEffort: reasoningEffortForModel(c.Model),
		ResponseFormat: &responseFormat{
			Type: "json_object",
		},
	}
	raw, err := c.doChat(ctx, body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Decisions []ParagraphDecision `json:"decisions"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse score json: %w", err)
	}
	for i := range parsed.Decisions {
		parsed.Decisions[i].Reason = normalizeDecisionReason(parsed.Decisions[i])
	}
	return parsed.Decisions, nil
}

type ParagraphDecision struct {
	ParagraphIndex int    `json:"paragraph_index"`
	Action         string `json:"action"`
	ReasonCode     string `json:"reason_code"`
	ReasonDetail   string `json:"reason_detail"`
	Reason         string `json:"reason"`
}

func (d *ParagraphDecision) UnmarshalJSON(data []byte) error {
	type Alias ParagraphDecision
	var raw struct {
		Alias
		ParagraphIndex json.RawMessage `json:"paragraph_index"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*d = ParagraphDecision(raw.Alias)

	s := strings.TrimSpace(string(raw.ParagraphIndex))
	if s == "" || s == "null" {
		return nil
	}
	s = strings.Trim(s, `"`)
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return fmt.Errorf("paragraph_index %q is not a valid integer", s)
		}
		n = n*10 + int(r-'0')
	}
	d.ParagraphIndex = n
	return nil
}

// sanitizeMessageContent strips NULs and invalid UTF-8 so the JSON body is always valid
// for strict API parsers (OpenAI rejects some malformed strings).
func sanitizeMessageContent(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return strings.ToValidUTF8(s, "\uFFFD")
}

func sanitizeChatRequest(req chatRequest) chatRequest {
	msgs := make([]chatMessage, len(req.Messages))
	for i := range req.Messages {
		msgs[i] = chatMessage{
			Role:    req.Messages[i].Role,
			Content: sanitizeMessageContent(req.Messages[i].Content),
		}
	}
	req.Messages = msgs
	stream := false
	req.Stream = &stream
	return req
}

// stripUTF8BOM removes a leading UTF-8 BOM if present (some proxies add it).
func stripUTF8BOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

func previewBytes(b []byte, max int) string {
	b = bytes.TrimSpace(b)
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// looksLikeSSE reports whether the body looks like server-sent events, not a JSON object.
func looksLikeSSE(b []byte) bool {
	s := string(bytes.TrimSpace(b))
	if len(s) == 0 {
		return false
	}
	if strings.HasPrefix(s, "event:") || strings.HasPrefix(s, "data:") {
		return true
	}
	// Multiline SSE often starts with a comment or event line after leading newline
	if strings.Contains(s, "\nevent:") || strings.Contains(s, "\ndata:") {
		return true
	}
	return false
}

func unmarshalChatCompletionBody(body []byte, statusCode int) (chatResponse, error) {
	body = stripUTF8BOM(bytes.TrimSpace(body))
	if len(body) == 0 {
		return chatResponse{}, fmt.Errorf("empty response body (http %d)", statusCode)
	}
	if looksLikeSSE(body) {
		return chatResponse{}, fmt.Errorf(
			"received an SSE/streaming response instead of JSON; ensure OPENAI_BASE_URL points to the chat completions API and streaming is disabled (got: %q)",
			previewBytes(body, 120),
		)
	}
	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return chatResponse{}, fmt.Errorf(
			"response is not valid JSON (http %d): %w; preview: %q",
			statusCode,
			err,
			previewBytes(body, 220),
		)
	}
	return cr, nil
}

func (c *Client) doChat(ctx context.Context, req chatRequest) (json.RawMessage, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()

	req = sanitizeChatRequest(req)
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("internal error: chat request JSON failed validation after sanitize")
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")

		res, err := c.HTTP.Do(httpReq)
		if err != nil {
			lastErr = err
			if attempt < 3 && retryableRequestErr(err) {
				if err := sleepWithContext(ctx, time.Duration(attempt)*750*time.Millisecond); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}

		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < 3 && retryableStatus(res.StatusCode) {
				if err := sleepWithContext(ctx, time.Duration(attempt)*750*time.Millisecond); err != nil {
					return nil, err
				}
				continue
			}
			return nil, readErr
		}

		cr, err := unmarshalChatCompletionBody(body, res.StatusCode)
		if err != nil {
			lastErr = fmt.Errorf("openai response: %w", err)
			if attempt < 3 && retryableResponseParseErr(res.StatusCode, err) {
				if err := sleepWithContext(ctx, time.Duration(attempt)*750*time.Millisecond); err != nil {
					return nil, err
				}
				continue
			}
			return nil, lastErr
		}
		if cr.Error != nil {
			lastErr = fmt.Errorf("openai: %s", cr.Error.Message)
			if attempt < 3 && retryableOpenAIError(res.StatusCode, cr.Error.Message) {
				if err := sleepWithContext(ctx, time.Duration(attempt)*750*time.Millisecond); err != nil {
					return nil, err
				}
				continue
			}
			return nil, lastErr
		}
		if res.StatusCode >= 400 {
			lastErr = fmt.Errorf("openai http %d: %s", res.StatusCode, string(body))
			if attempt < 3 && retryableStatus(res.StatusCode) {
				if err := sleepWithContext(ctx, time.Duration(attempt)*750*time.Millisecond); err != nil {
					return nil, err
				}
				continue
			}
			return nil, lastErr
		}
		if len(cr.Choices) == 0 {
			return nil, fmt.Errorf("openai: no choices")
		}
		content := strings.TrimSpace(cr.Choices[0].Message.Content)
		return json.RawMessage(content), nil
	}
	return nil, lastErr
}

func retryableRequestErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return code >= 500
	}
}

func retryableResponseParseErr(status int, err error) bool {
	if retryableStatus(status) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return status == http.StatusBadRequest &&
		strings.Contains(msg, "could not parse the json body of your request")
}

func retryableOpenAIError(status int, message string) bool {
	if retryableStatus(status) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	return status == http.StatusBadRequest &&
		strings.Contains(msg, "could not parse the json body of your request")
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func normalizeDecisionReason(d ParagraphDecision) string {
	action := strings.ToLower(strings.TrimSpace(d.Action))
	code := strings.ToLower(strings.TrimSpace(d.ReasonCode))
	detail := strings.TrimSpace(d.ReasonDetail)
	freeform := strings.TrimSpace(d.Reason)
	withDetail := func(prefix string) string {
		if detail == "" {
			return prefix
		}
		return fmt.Sprintf("%s: %s", prefix, detail)
	}

	switch action {
	case "omit":
		switch code {
		case "avoid_topic":
			if detail != "" {
				return fmt.Sprintf("mostly about %s", detail)
			}
			return "mostly about an avoided topic"
		case "low_value_tangent":
			if detail != "" {
				return fmt.Sprintf("side tangent about %s", detail)
			}
			return "side tangent"
		case "repetition":
			return withDetail("repeats nearby material")
		case "low_priority_background":
			if detail != "" {
				return fmt.Sprintf("background detail about %s", detail)
			}
			return "background detail"
		case "weak_lean_match":
			if detail != "" {
				return fmt.Sprintf("weak match for %s", detail)
			}
			return "weak match for the reader focus"
		}
	case "keep":
		switch code {
		case "lean_match":
			return withDetail("matches what the reader wants more of")
		case "main_thread":
			return withDetail("part of the main thread")
		case "setup_payoff":
			return withDetail("important setup or payoff")
		case "context":
			return withDetail("needed for context")
		case "fallback_keep":
			return "kept by default"
		}
	}

	if freeform != "" {
		low := strings.ToLower(freeform)
		switch {
		case strings.Contains(low, "avoid"):
			return "matches a topic the reader wants to avoid"
		case strings.Contains(low, "repeat"):
			return "repeats material already covered"
		case strings.Contains(low, "tangent"):
			return "is a tangent, not a priority for this reader"
		case strings.Contains(low, "context"):
			return "needed for context"
		case strings.Contains(low, "main thread"), strings.Contains(low, "core"):
			return "part of the main thread"
		}
		return freeform
	}

	if action == "omit" {
		return "lower priority than surrounding material"
	}
	return "kept by default"
}
