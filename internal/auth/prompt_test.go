package auth_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
)

func TestPromptTokenReadsOneLineFromAPipe(t *testing.T) {
	var out bytes.Buffer
	tok, err := auth.PromptToken(strings.NewReader("  github_pat_abc \r\nrest\n"), &out, notes)
	if err != nil || tok != "github_pat_abc" {
		t.Fatalf("PromptToken = %q, %v; want github_pat_abc", tok, err)
	}
	if !strings.Contains(out.String(), "personal-access-tokens/new") {
		t.Errorf("prompt %q does not say where to create a token", out.String())
	}
	if strings.Contains(out.String(), "github_pat_abc") {
		t.Errorf("prompt echoed the token: %q", out.String())
	}
}

func TestPromptTokenRejectsEmptyInput(t *testing.T) {
	if _, err := auth.PromptToken(strings.NewReader("\n"), &bytes.Buffer{}, notes); err == nil {
		t.Fatal("PromptToken accepted an empty token")
	}
}
