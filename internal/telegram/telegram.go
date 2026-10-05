// Package telegram implements the bot's Telegram transport: long-polling for
// incoming messages and photos, and sending text and PDF replies back.
//
// It talks to the Bot API over plain HTTP rather than through a wrapper
// library. Only four endpoints are needed (getUpdates, sendMessage,
// sendDocument, getFile), the API is stable and well documented, and this
// avoids taking on a dependency whose release cadence we don't control.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/nurfaizh/keuanganku/internal/messaging"
)

const (
	apiBase = "https://api.telegram.org"

	// pollTimeout is the long-poll window held open by getUpdates. The HTTP
	// client's own timeout must exceed it.
	pollTimeout = 50 * time.Second

	// maxPhotoBytes caps what we'll download from a photo message, so an
	// oversized upload can't balloon memory.
	maxPhotoBytes = 12 << 20 // 12 MiB
)

// Bot is a Telegram transport. It satisfies messaging.Sender.
type Bot struct {
	token  string
	client *http.Client
}

// New builds a Bot. The HTTP client's timeout leaves room for the long poll.
func New(token string) *Bot {
	return &Bot{
		token:  token,
		client: &http.Client{Timeout: pollTimeout + 30*time.Second},
	}
}

// Me returns the bot's own username, confirming the token works. Calling it
// at startup turns an invalid token into an immediate, obvious failure
// instead of a polling loop that silently never receives anything.
func (b *Bot) Me(ctx context.Context) (string, error) {
	raw, err := b.call(ctx, "getMe", map[string]any{})
	if err != nil {
		return "", err
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &me); err != nil {
		return "", fmt.Errorf("decode getMe: %w", err)
	}
	return me.Username, nil
}

// Message is one inbound Telegram message, normalised for the handler.
type Message struct {
	ChatID int64
	// From is the sender's username or display name, for logging only.
	From string
	Text string
	// Photo is the largest available size of an attached photo, already
	// downloaded. Nil when the message carried no photo.
	Photo     []byte
	PhotoMIME string
}

// Handler processes one inbound message.
type Handler func(ctx context.Context, msg Message)

// Run long-polls for updates until ctx is cancelled, dispatching each message
// to handle. Transient failures are logged by the caller's handler; polling
// itself backs off rather than spinning.
func (b *Bot) Run(ctx context.Context, handle Handler, logf func(string, ...any)) {
	var offset int64
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		updates, err := b.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logf("telegram: getUpdates: %v (retrying in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			// Cap the backoff so a long outage doesn't leave the bot asleep.
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second

		for _, u := range updates {
			// Acknowledge every update, even ones we ignore, or the same
			// batch is redelivered forever.
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			msg := u.Message
			if msg == nil {
				msg = u.EditedMessage
			}
			if msg == nil || msg.Chat == nil {
				continue
			}

			out := Message{
				ChatID: msg.Chat.ID,
				From:   msg.From.name(),
				Text:   msg.text(),
			}

			if fileID := msg.largestPhotoID(); fileID != "" {
				data, err := b.download(ctx, fileID)
				if err != nil {
					logf("telegram: download photo: %v", err)
					continue
				}
				out.Photo = data
				out.PhotoMIME = "image/jpeg" // Telegram re-encodes photos as JPEG
			}

			handle(ctx, out)
		}
	}
}

// SendText delivers a plain reply. The bot's messages are written in
// WhatsApp's *bold* / _italic_ convention, so they are converted to Telegram
// HTML on the way out.
func (b *Bot) SendText(ctx context.Context, chatID, text string) error {
	id, err := messaging.ParseTelegramChatID(chatID)
	if err != nil {
		return err
	}
	_, err = b.call(ctx, "sendMessage", map[string]any{
		"chat_id":    id,
		"text":       ToHTML(text),
		"parse_mode": "HTML",
	})
	return err
}

// SendDocument uploads a file (the monthly PDF, or an on-demand recap).
func (b *Bot) SendDocument(ctx context.Context, chatID string, data []byte, filename, caption string) error {
	id, err := messaging.ParseTelegramChatID(chatID)
	if err != nil {
		return err
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("chat_id", strconv.FormatInt(id, 10)); err != nil {
		return err
	}
	if caption != "" {
		if err := w.WriteField("caption", ToHTML(caption)); err != nil {
			return err
		}
		if err := w.WriteField("parse_mode", "HTML"); err != nil {
			return err
		}
	}
	part, err := w.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint("sendDocument"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("sendDocument: %w", err)
	}
	defer resp.Body.Close()
	return decodeResult(resp, nil)
}

func (b *Bot) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", apiBase, b.token, method)
}

// call posts a JSON request and decodes the envelope into out (which may be nil).
func (b *Bot) call(ctx context.Context, method string, payload map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint(method), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	var raw json.RawMessage
	if err := decodeResult(resp, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return raw, nil
}

// apiResponse is the Bot API's envelope around every result.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

// APIError is a refusal from Telegram itself, as opposed to a network
// failure. The distinction matters at startup: a rejected token is fatal and
// should stop the bot, while a connection blip is not — the poll loop already
// retries those.
type APIError struct {
	Code        int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram error %d: %s", e.Code, e.Description)
}

func decodeResult(resp *http.Response, out *json.RawMessage) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env apiResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode response (http %d): %w", resp.StatusCode, err)
	}
	if !env.OK {
		return &APIError{Code: env.ErrorCode, Description: env.Description}
	}
	if out != nil {
		*out = env.Result
	}
	return nil
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	raw, err := b.call(ctx, "getUpdates", map[string]any{
		"offset":  offset,
		"timeout": int(pollTimeout / time.Second),
		// Only the update kinds this bot acts on.
		"allowed_updates": []string{"message", "edited_message"},
	})
	if err != nil {
		return nil, err
	}
	var updates []update
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, fmt.Errorf("decode updates: %w", err)
	}
	return updates, nil
}

// download resolves a file ID to its path and fetches the bytes.
func (b *Bot) download(ctx context.Context, fileID string) ([]byte, error) {
	raw, err := b.call(ctx, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return nil, err
	}
	var f struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("decode file: %w", err)
	}
	if f.FilePath == "" {
		return nil, fmt.Errorf("telegram returned no file path for %s", fileID)
	}
	if f.FileSize > maxPhotoBytes {
		return nil, fmt.Errorf("photo is %d bytes, over the %d limit", f.FileSize, maxPhotoBytes)
	}

	u := fmt.Sprintf("%s/file/bot%s/%s", apiBase, b.token, f.FilePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch file: http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPhotoBytes))
}

// Wire types. Only the fields this bot reads are declared.

type update struct {
	UpdateID      int64    `json:"update_id"`
	Message       *message `json:"message"`
	EditedMessage *message `json:"edited_message"`
}

type message struct {
	Chat    *chat       `json:"chat"`
	From    *user       `json:"from"`
	Text    string      `json:"text"`
	Caption string      `json:"caption"`
	Photo   []photoSize `json:"photo"`
}

type chat struct {
	ID int64 `json:"id"`
}

type user struct {
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type photoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
}

func (u *user) name() string {
	if u == nil {
		return "unknown"
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return u.FirstName
}

// text returns the message body, falling back to a photo's caption.
func (m *message) text() string {
	if m.Text != "" {
		return m.Text
	}
	return m.Caption
}

// largestPhotoID picks the highest-resolution rendition Telegram offers,
// which is the one worth running OCR against. Returns "" when there is no
// photo.
func (m *message) largestPhotoID() string {
	var best photoSize
	for _, p := range m.Photo {
		if p.Width > best.Width {
			best = p
		}
	}
	return best.FileID
}
