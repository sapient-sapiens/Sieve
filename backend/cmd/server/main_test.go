package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidSpeakerSplit(t *testing.T) {
	for _, v := range []string{"auto", "on", "off", " AUTO "} {
		if !validSpeakerSplit(v) {
			t.Fatalf("expected %q to be valid", v)
		}
	}
	if validSpeakerSplit("weird") {
		t.Fatal("unexpected valid speaker_split")
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	err := os.WriteFile(path, []byte("OPENAI_MODEL=test-model\nexport OPENAI_BASE_URL=\"https://example.com/v1\"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("OPENAI_MODEL", "")
	_ = os.Unsetenv("OPENAI_MODEL")
	_ = os.Unsetenv("OPENAI_BASE_URL")

	loadEnvFile(path)

	if got := os.Getenv("OPENAI_MODEL"); got != "test-model" {
		t.Fatalf("OPENAI_MODEL=%q", got)
	}
	if got := os.Getenv("OPENAI_BASE_URL"); got != "https://example.com/v1" {
		t.Fatalf("OPENAI_BASE_URL=%q", got)
	}
}
