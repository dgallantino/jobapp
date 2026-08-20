package llm

import (
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
