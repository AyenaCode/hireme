// Package notify pushes job matches to a Telegram chat via the Bot API.
// It uses HTML parse mode, which has the simplest escaping rules (only < > &),
// and disables link previews to keep messages compact.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"hireme/internal/jsearch"
)

// maxMessageLen is Telegram's hard limit per message (4096 chars). We stay well
// under it by trimming, but guard against oversized titles/employers.
const maxMessageLen = 4096

// Telegram sends messages to a single chat.
type Telegram struct {
	token  string
	chatID string
	http   *http.Client
}

// New builds a Telegram notifier.
func New(token, chatID string) *Telegram {
	return &Telegram{
		token:  token,
		chatID: chatID,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

type sendMessageRequest struct {
	ChatID              string `json:"chat_id"`
	Text                string `json:"text"`
	ParseMode           string `json:"parse_mode"`
	DisableWebPreview   bool   `json:"disable_web_page_preview"`
	DisableNotifsSilent bool   `json:"disable_notification,omitempty"`
}

type sendMessageResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// SendJob formats and sends one job to the chat.
func (t *Telegram) SendJob(ctx context.Context, j jsearch.Job) error {
	return t.send(ctx, formatJob(j))
}

// SendText sends a plain operational message (e.g. a quota warning) to the chat.
// The caller must pre-escape any HTML-special characters it does not intend as
// markup; the strings we pass are static and contain none.
func (t *Telegram) SendText(ctx context.Context, text string) error {
	return t.send(ctx, text)
}

func (t *Telegram) send(ctx context.Context, text string) error {
	if len(text) > maxMessageLen {
		text = text[:maxMessageLen]
	}

	payload, err := json.Marshal(sendMessageRequest{
		ChatID:            t.chatID,
		Text:              text,
		ParseMode:         "HTML",
		DisableWebPreview: true,
	})
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("call telegram: %w", err)
	}
	defer resp.Body.Close()

	var out sendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode telegram response: %w", err)
	}
	if !out.OK {
		return fmt.Errorf("telegram error %d: %s", out.ErrorCode, out.Description)
	}
	return nil
}

// formatJob renders a job as an HTML Telegram message.
func formatJob(j jsearch.Job) string {
	var b strings.Builder

	fmt.Fprintf(&b, "<b>%s</b>\n", esc(j.Title))
	if j.Employer != "" {
		fmt.Fprintf(&b, "🏢 %s\n", esc(j.Employer))
	}

	loc := j.Location
	if j.Remote {
		if loc == "" {
			loc = "Remote"
		} else {
			loc += " · Remote"
		}
	}
	if loc != "" {
		fmt.Fprintf(&b, "📍 %s\n", esc(loc))
	}
	if j.EmployType != "" {
		fmt.Fprintf(&b, "💼 %s\n", esc(j.EmployType))
	}
	if sal := salary(j); sal != "" {
		fmt.Fprintf(&b, "💰 %s\n", esc(sal))
	}
	if j.Publisher != "" {
		fmt.Fprintf(&b, "🔎 via %s\n", esc(j.Publisher))
	}

	link := j.ApplyLink
	if link == "" {
		link = j.GoogleLink
	}
	if link != "" {
		fmt.Fprintf(&b, "\n<a href=\"%s\">Apply →</a>", esc(link))
	}
	return b.String()
}

func salary(j jsearch.Job) string {
	if j.MinSalary == nil && j.MaxSalary == nil {
		return ""
	}
	period := j.SalaryPeriod
	switch {
	case j.MinSalary != nil && j.MaxSalary != nil:
		return fmt.Sprintf("%.0f–%.0f %s", *j.MinSalary, *j.MaxSalary, period)
	case j.MinSalary != nil:
		return fmt.Sprintf("from %.0f %s", *j.MinSalary, period)
	default:
		return fmt.Sprintf("up to %.0f %s", *j.MaxSalary, period)
	}
}

// esc escapes the three characters that are special in Telegram HTML mode.
func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
