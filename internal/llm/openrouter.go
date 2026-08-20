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

const extractJobFieldsSystem = "You extract job posting fields from unstructured social media text. " +
	"Use only the post; never invent employers, job titles, or salaries. " +
	"If a field is not clearly present, return an empty string. " +
	"Output a JSON object only, no markdown fences."

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
	system, user := buildCoverLetterPrompt(c.coverLetterSystem(), profile, title, company, description)
	content, err := c.complete(ctx, system, user)
	if err != nil {
		return "", err
	}
	if sig := strings.TrimSpace(profile["signature"]); sig != "" {
		content = content + "\n\n" + sig
	}
	return content, nil
}

// ExtractJobFields asks the model to fill the named empty fields from postText.
// missing should be a subset of title, company, salary. Unknown fields stay empty.
func (c *Client) ExtractJobFields(ctx context.Context, postText string, missing []string) (title, company, salary string, err error) {
	if len(missing) == 0 {
		return "", "", "", nil
	}
	system, user := buildExtractPrompt(postText, missing)
	content, err := c.complete(ctx, system, user)
	if err != nil {
		return "", "", "", err
	}
	return decodeJobFieldsJSON(content)
}

func (c *Client) complete(ctx context.Context, system, user string) (string, error) {
	if c == nil || c.APIKey == "" {
		return "", fmt.Errorf("OPENROUTER_API_KEY is not set")
	}

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
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func buildExtractPrompt(postText string, missing []string) (string, string) {
	var b strings.Builder
	b.WriteString("Extract only these fields from the post: ")
	b.WriteString(strings.Join(missing, ", "))
	b.WriteString(".\nReturn JSON with those keys. Use empty strings when unknown.\n\nPost:\n")
	b.WriteString(postText)
	return extractJobFieldsSystem, b.String()
}

func decodeJobFieldsJSON(content string) (title, company, salary string, err error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```JSON")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	if i := strings.Index(content, "{"); i >= 0 {
		if j := strings.LastIndex(content, "}"); j > i {
			content = content[i : j+1]
		}
	}
	var parsed struct {
		Title   string `json:"title"`
		Company string `json:"company"`
		Salary  string `json:"salary"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return "", "", "", fmt.Errorf("decode job fields: %w", err)
	}
	return strings.TrimSpace(parsed.Title), strings.TrimSpace(parsed.Company), strings.TrimSpace(parsed.Salary), nil
}

func (c *Client) coverLetterSystem() string {
	if c != nil {
		if s := strings.TrimSpace(c.SystemPrompt); s != "" {
			return s
		}
	}
	return defaultCoverLetterSystem
}

// buildCoverLetterPrompt constructs system/user messages for cover letter generation.
func buildCoverLetterPrompt(system string, profile map[string]string, title, company, description string) (string, string) {
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
