package llm

import (
	"strings"
	"testing"
)

func TestDecodeJobFieldsJSON(t *testing.T) {
	title, company, salary, err := decodeJobFieldsJSON("```json\n{\"title\":\"Line Cook\",\"company\":\"Harbor Bites\",\"salary\":\"\"}\n```")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if title != "Line Cook" || company != "Harbor Bites" || salary != "" {
		t.Errorf("got title=%q company=%q salary=%q", title, company, salary)
	}
}

func TestDecodeJobFieldsJSON_SurroundingText(t *testing.T) {
	title, company, salary, err := decodeJobFieldsJSON("Here you go:\n{\"title\":\"\",\"company\":\"Acme\",\"salary\":\"UMR\"}\nThanks")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if title != "" || company != "Acme" || salary != "UMR" {
		t.Errorf("got title=%q company=%q salary=%q", title, company, salary)
	}
}

func TestExtractJobFields_NoAPIKey(t *testing.T) {
	c := NewClient("", "openai/gpt-4o-mini", "")
	_, _, _, err := c.ExtractJobFields(t.Context(), "We're hiring a cook", []string{"title"})
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestExtractJobFields_NoMissing(t *testing.T) {
	c := NewClient("key", "openai/gpt-4o-mini", "")
	title, company, salary, err := c.ExtractJobFields(t.Context(), "post", nil)
	if err != nil {
		t.Fatalf("empty missing should not call API: %v", err)
	}
	if title != "" || company != "" || salary != "" {
		t.Errorf("got title=%q company=%q salary=%q", title, company, salary)
	}
}

func TestComposeCoverLetterPrompt(t *testing.T) {
	profile := map[string]string{
		"full_name":         "Ada Lovelace",
		"summary":           "Mathematician",
		"work_history":      "Analytical Engine",
		"skills":            "Algorithms",
		"tone_preferences":  "Clear and direct",
		"signature":         "Ada L.",
		"extra_note":        "Remote OK",
		"empty_extra":       "  ",
	}
	got := ComposeCoverLetterPrompt("Custom system instructions.", profile)

	if !strings.HasPrefix(got, "Custom system instructions.\n\n") {
		t.Fatalf("expected custom system prompt prefix, got %q", got[:min(80, len(got))])
	}
	for _, want := range []string{
		"Candidate profile:\n",
		"- full_name: Ada Lovelace\n",
		"- summary: Mathematician\n",
		"- work_history: Analytical Engine\n",
		"- skills: Algorithms\n",
		"- tone_preferences: Clear and direct\n",
		"- extra_note: Remote OK\n",
		"\nJob:\n",
		"Title: \n",
		"Company: \n",
		"Description:\n",
		"\n\nWrite a tailored cover letter.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "signature") || strings.Contains(got, "Ada L.") {
		t.Errorf("signature must be excluded from prompt:\n%s", got)
	}
	if strings.Contains(got, "empty_extra") {
		t.Errorf("empty extra keys must be skipped:\n%s", got)
	}
}

func TestComposeCoverLetterPrompt_FallbackSystem(t *testing.T) {
	got := ComposeCoverLetterPrompt("", map[string]string{"full_name": "Ada"})
	if !strings.HasPrefix(got, defaultCoverLetterSystem+"\n\n") {
		t.Fatalf("expected default system prompt, got %q", got[:min(120, len(got))])
	}
}

func TestClientComposeCoverLetterPrompt_NoAPIKey(t *testing.T) {
	c := NewClient("", "openai/gpt-4o-mini", "Use this system.")
	got := c.ComposeCoverLetterPrompt(map[string]string{"full_name": "Ada"})
	if !strings.Contains(got, "Use this system.") || !strings.Contains(got, "- full_name: Ada\n") {
		t.Fatalf("unexpected prompt:\n%s", got)
	}
}
