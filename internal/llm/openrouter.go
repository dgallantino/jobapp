package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const openRouterURL = "https://openrouter.ai/api/v1/chat/completions"

// defaultCoverLetterSystem is used when Client.SystemPrompt is empty.
const defaultCoverLetterSystem = "You write tailored cover letters for job applications. " +
	"Use only the candidate profile and job description given; never invent employers, degrees, skills, or experience. " +
	"Output the letter body only, no markdown fences."

// Client calls OpenRouter's OpenAI-compatible chat completions API.
type Client struct {
	APIKey       string
	Model        string
	SystemPrompt string // cover-letter system prompt; empty uses defaultCoverLetterSystem
	HTTP         *http.Client
}

// NewClient constructs an OpenRouter client.
func NewClient(apiKey, model, systemPrompt string) *Client {
	return &Client{
		APIKey:       apiKey,
		Model:        model,
		SystemPrompt: strings.TrimSpace(systemPrompt),
		HTTP:         &http.Client{Timeout: 120 * time.Second},
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// GenerateCoverLetter builds a prompt from profile + job ad and calls OpenRouter.
func (c *Client) GenerateCoverLetter(ctx context.Context, profile map[string]string, title, company, description string) (string, error) {
	if c.APIKey == "" {
		return "", fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	system, user := buildPrompt(c.coverLetterSystem(), profile, title, company, description)

	body, err := json.Marshal(chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("HTTP-Referer", "jobapp.localhost")
	req.Header.Set("X-Title", "jobapp")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("openrouter HTTP %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode openrouter response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("openrouter: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openrouter: empty choices")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if sig := strings.TrimSpace(profile["signature"]); sig != "" {
		content = content + "\n\n" + sig
	}
	return content, nil
}

func (c *Client) coverLetterSystem() string {
	if c != nil {
		if s := strings.TrimSpace(c.SystemPrompt); s != "" {
			return s
		}
	}
	return defaultCoverLetterSystem
}

// buildPrompt constructs system/user messages for cover letter generation.
func buildPrompt(system string, profile map[string]string, title, company, description string) (string, string) {
	var b strings.Builder
	b.WriteString("Candidate profile:\n")
	for _, key := range []string{"full_name", "summary", "work_history", "skills", "tone_preferences"} {
		if v := strings.TrimSpace(profile[key]); v != "" {
			b.WriteString("- ")
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	// Include any extra profile keys not in the default list.
	for k, v := range profile {
		switch k {
		case "full_name", "summary", "work_history", "skills", "tone_preferences", "signature":
			continue
		}
		if strings.TrimSpace(v) == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\n")
	}

	b.WriteString("\nJob:\n")
	b.WriteString("Title: ")
	b.WriteString(title)
	b.WriteString("\nCompany: ")
	b.WriteString(company)
	b.WriteString("\nDescription:\n")
	b.WriteString(description)
	b.WriteString("\n\nWrite a tailored cover letter.")
	return system, b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
