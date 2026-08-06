package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobapp/internal/models"
	"jobapp/internal/scrape"
)

// STUB: reply wording not finalized — keep as constants so they can change later.
const (
	ReplySuccess = "Processed ✅"
	// Assumption: reply on scrape failure so the user knows the bot saw the message.
	ReplyFailure = "Couldn't process ⚠️"
)

var urlRe = regexp.MustCompile(`https?://[^\s<>"']+`)

// Client talks to the Telegram Bot API with short-poll getUpdates.
type Client struct {
	Token  string
	ChatID int64
	HTTP   *http.Client
}

// NewClient constructs a Telegram API client.
func NewClient(token string, chatID int64) *Client {
	return &Client{
		Token:  token,
		ChatID: chatID,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
	}
}

type getUpdatesResponse struct {
	OK     bool     `json:"ok"`
	Result []Update `json:"result"`
}

// Update is a Telegram update.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Message is a Telegram message.
type Message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      Chat   `json:"chat"`
}

// Chat identifies a Telegram chat.
type Chat struct {
	ID int64 `json:"id"`
}

// CheckResult summarizes one telegram-check pass.
type CheckResult struct {
	MessagesChecked int
	LinksFound      int
	LinksScraped    int
	Errors          int
}

// RunCheck short-polls getUpdates, scrapes job links, replies, advances state.
func RunCheck(ctx context.Context, db *sql.DB, reg *scrape.Registry, c *Client) (CheckResult, error) {
	if c.Token == "" {
		return CheckResult{}, fmt.Errorf("TELEGRAM_BOT_TOKEN is not set")
	}
	if c.ChatID == 0 {
		// STUB: chat ID must be supplied by the user.
		return CheckResult{}, fmt.Errorf("TELEGRAM_CHAT_ID is not set")
	}

	st, err := models.GetTelegramState(ctx, db)
	if err != nil {
		return CheckResult{}, err
	}

	updates, err := c.getUpdates(ctx, st.LastUpdateID+1)
	if err != nil {
		return CheckResult{}, err
	}

	sourceID, err := models.EnsureTelegramSource(ctx, db)
	if err != nil {
		return CheckResult{}, err
	}

	var res CheckResult
	var maxUpdateID = st.LastUpdateID

	for _, u := range updates {
		if u.UpdateID > maxUpdateID {
			maxUpdateID = u.UpdateID
		}
		if u.Message == nil {
			continue
		}
		if u.Message.Chat.ID != c.ChatID {
			continue
		}
		res.MessagesChecked++

		urls := extractURLs(u.Message.Text)
		if len(urls) == 0 {
			continue
		}
		res.LinksFound += len(urls)

		// TODO: confirm desired behavior for multi-link messages
		// Default: process all links found in the message and send one reply after all are done.
		anyOK := false
		anyFail := false
		for _, link := range urls {
			sid := sourceID
			_, inserted, err := scrape.ScrapeAndStore(ctx, db, reg, link, &sid)
			if err != nil {
				log.Printf("telegram scrape %s: %v", link, err)
				res.Errors++
				anyFail = true
				continue
			}
			if inserted {
				res.LinksScraped++
			}
			anyOK = true
			_ = inserted
		}

		reply := ReplySuccess
		if !anyOK && anyFail {
			reply = ReplyFailure
		} else if anyOK && anyFail {
			// Assumption: partial success still gets success wording; failures are in logs.
			reply = ReplySuccess
		} else if !anyOK {
			continue
		}
		if err := c.sendReply(ctx, u.Message.Chat.ID, u.Message.MessageID, reply); err != nil {
			log.Printf("telegram reply: %v", err)
			res.Errors++
		}
	}

	if maxUpdateID > st.LastUpdateID {
		if err := models.SetTelegramLastUpdateID(ctx, db, maxUpdateID); err != nil {
			return res, err
		}
	}

	log.Printf("telegram-check summary: messages=%d links_found=%d links_scraped=%d errors=%d last_update_id=%d",
		res.MessagesChecked, res.LinksFound, res.LinksScraped, res.Errors, maxUpdateID)
	return res, nil
}

func (c *Client) getUpdates(ctx context.Context, offset int64) ([]Update, error) {
	// Short-poll: timeout=0 returns immediately with pending updates.
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", "0")
	q.Set("allowed_updates", `["message"]`)

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", c.Token, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram getUpdates HTTP %d: %s", resp.StatusCode, string(body))
	}
	var parsed getUpdatesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if !parsed.OK {
		return nil, fmt.Errorf("telegram getUpdates not ok: %s", string(body))
	}
	return parsed.Result, nil
}

func (c *Client) sendReply(ctx context.Context, chatID, replyTo int64, text string) error {
	q := url.Values{}
	q.Set("chat_id", strconv.FormatInt(chatID, 10))
	q.Set("text", text)
	q.Set("reply_to_message_id", strconv.FormatInt(replyTo, 10))

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?%s", c.Token, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram sendMessage HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func extractURLs(text string) []string {
	raw := urlRe.FindAllString(text, -1)
	seen := map[string]struct{}{}
	var out []string
	for _, u := range raw {
		u = strings.TrimRight(u, ".,);]")
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}
