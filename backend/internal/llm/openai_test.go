package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeDecisionReason(t *testing.T) {
	tests := []struct {
		name string
		in   ParagraphDecision
		want string
	}{
		{
			name: "avoid topic with detail",
			in: ParagraphDecision{
				Action:       "omit",
				ReasonCode:   "avoid_topic",
				ReasonDetail: "sports talk",
			},
			want: "mostly about sports talk",
		},
		{
			name: "generic omit fallback",
			in: ParagraphDecision{
				Action: "omit",
			},
			want: "lower priority than surrounding material",
		},
		{
			name: "keep main thread with detail",
			in: ParagraphDecision{
				Action:       "keep",
				ReasonCode:   "main_thread",
				ReasonDetail: "core election discussion",
			},
			want: "part of the main thread: core election discussion",
		},
		{
			name: "freeform repetition fallback",
			in: ParagraphDecision{
				Action: "omit",
				Reason: "mostly repeats earlier points",
			},
			want: "repeats material already covered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDecisionReason(tt.in); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestReasoningEffortForModel(t *testing.T) {
	if got := reasoningEffortForModel("gpt-5-mini"); got != "low" {
		t.Fatalf("got %q", got)
	}
	if got := reasoningEffortForModel("gpt-4o-mini"); got != "" {
		t.Fatalf("unexpected value %q", got)
	}
}

func TestLooksLikeSSE(t *testing.T) {
	if !looksLikeSSE([]byte("event: message\ndata: {}\n\n")) {
		t.Fatal("expected SSE prefix")
	}
	if !looksLikeSSE([]byte("data: {\"x\":1}\n")) {
		t.Fatal("expected data: prefix")
	}
	if looksLikeSSE([]byte(`{"choices":[]}`)) {
		t.Fatal("JSON should not be SSE")
	}
}

func TestUnmarshalChatCompletionBody(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		raw := `{"choices":[{"message":{"content":"{}"}}]}`
		cr, err := unmarshalChatCompletionBody([]byte(raw), 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(cr.Choices) != 1 {
			t.Fatalf("choices: %+v", cr)
		}
	})
	t.Run("sse", func(t *testing.T) {
		_, err := unmarshalChatCompletionBody([]byte("event: error\ndata: {}\n"), 200)
		if err == nil || !strings.Contains(err.Error(), "SSE/streaming") {
			t.Fatalf("want SSE error, got: %v", err)
		}
	})
	t.Run("plain_text", func(t *testing.T) {
		_, err := unmarshalChatCompletionBody([]byte("error: upstream unavailable"), 502)
		if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
			t.Fatalf("want json error, got: %v", err)
		}
	})
}

func TestSanitizeMessageContent(t *testing.T) {
	if got := sanitizeMessageContent("hello\x00world"); got != "helloworld" {
		t.Fatalf("NUL strip: %q", got)
	}
}

func TestSanitizeChatRequestSetsStreamFalse(t *testing.T) {
	req := chatRequest{
		Model: "gpt-4o-mini",
		Messages: []chatMessage{
			{Role: "user", Content: "hi"},
		},
	}
	req = sanitizeChatRequest(req)
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"stream":false`) {
		t.Fatalf("expected stream false in JSON: %s", string(b))
	}
	if !json.Valid(b) {
		t.Fatal("invalid JSON")
	}
}

func TestRetryableResponseParseErr(t *testing.T) {
	if !retryableResponseParseErr(502, assertErr("response is not valid JSON")) {
		t.Fatal("expected retryable 502 parse error")
	}
	if retryableResponseParseErr(400, assertErr("some other parse failure")) {
		t.Fatal("unexpected retryable 400 parse error")
	}
}

func TestRetryableOpenAIError(t *testing.T) {
	msg := "We could not parse the JSON body of your request."
	if !retryableOpenAIError(400, msg) {
		t.Fatal("expected retryable parse-body 400")
	}
	if retryableOpenAIError(400, "invalid_request_error") {
		t.Fatal("unexpected retryable generic 400")
	}
	if !retryableOpenAIError(503, "service unavailable") {
		t.Fatal("expected retryable 503")
	}
}

func assertErr(msg string) error { return &staticErr{msg: msg} }

type staticErr struct{ msg string }

func (e *staticErr) Error() string { return e.msg }
