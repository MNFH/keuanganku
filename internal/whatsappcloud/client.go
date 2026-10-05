// Package whatsappcloud implements the official WhatsApp Business Cloud API
// transport: a webhook that receives messages and a Graph API client that
// sends replies.
//
// Unlike the whatsmeow transport, this is a sanctioned bot — no linked
// device, no QR, no ban risk — but it comes with a rule that shapes the code:
// outside a 24-hour window opened by the *user's* own message, only
// pre-approved templates may be sent. Free-form text and documents are
// rejected. Sender surfaces that as ErrOutsideWindow, and implements
// messaging.Reengager so callers can nudge the user instead of silently
// failing.
package whatsappcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/messaging"
)

const (
	graphBase = "https://graph.facebook.com"

	// DefaultAPIVersion is the Graph API version used when none is
	// configured. Meta ships a new version roughly quarterly and retires old
	// ones, so this is overridable by environment.
	DefaultAPIVersion = "v26.0"

	// reengagementErrorCode is what Meta returns when a free-form message is
	// rejected because more than 24 hours have passed since the user last
	// wrote. See https://developers.facebook.com/docs/whatsapp/cloud-api/support/error-codes
	reengagementErrorCode = 131047

	maxMediaBytes = 16 << 20 // generous for a PDF or a receipt photo
)

// ErrOutsideWindow wraps messaging.ErrOutsideWindow so callers can match the
// shared sentinel without importing this package.
var ErrOutsideWindow = fmt.Errorf("%w: 24-hour customer service window", messaging.ErrOutsideWindow)

// Config holds the credentials from the Meta app dashboard.
type Config struct {
	// AccessToken is a system-user or permanent token with whatsapp_business_messaging.
	AccessToken string
	// PhoneNumberID identifies the sending number (not the phone number itself).
	PhoneNumberID string
	// APIVersion defaults to DefaultAPIVersion.
	APIVersion string
	// ReengagementTemplate is the name of an approved Utility template used to
	// prompt a user whose window has closed. Optional; without it, a blocked
	// send is reported and dropped.
	ReengagementTemplate string
	// TemplateLanguage is the template's language code, e.g. "id" or "en".
	TemplateLanguage string
}

// Client talks to the Graph API. It satisfies messaging.Sender and
// messaging.Reengager.
type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	if cfg.APIVersion == "" {
		cfg.APIVersion = DefaultAPIVersion
	}
	if cfg.TemplateLanguage == "" {
		cfg.TemplateLanguage = "id"
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) endpoint(path string) string {
	return fmt.Sprintf("%s/%s/%s", graphBase, c.cfg.APIVersion, path)
}

// SendText delivers a plain-text reply.
//
// WhatsApp renders *bold* and _italic_ natively, which is the convention the
// bot's replies are already written in, so no conversion is needed here —
// unlike Telegram.
func (c *Client) SendText(ctx context.Context, chatID, text string) error {
	waID, err := messaging.ParseCloudChatID(chatID)
	if err != nil {
		return err
	}
	return c.postMessage(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                waID,
		"type":              "text",
		"text":              map[string]any{"body": text, "preview_url": false},
	})
}

// SendDocument uploads the file and sends it by media ID. Uploading rather
// than linking avoids needing to host the PDF on a public URL.
func (c *Client) SendDocument(ctx context.Context, chatID string, data []byte, filename, caption string) error {
	waID, err := messaging.ParseCloudChatID(chatID)
	if err != nil {
		return err
	}

	mediaID, err := c.uploadMedia(ctx, data, filename, "application/pdf")
	if err != nil {
		return fmt.Errorf("upload document: %w", err)
	}

	doc := map[string]any{"id": mediaID, "filename": filename}
	if caption != "" {
		doc["caption"] = caption
	}
	return c.postMessage(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                waID,
		"type":              "document",
		"document":          doc,
	})
}

// Reengage sends the configured template to a user whose 24-hour window has
// closed, asking them to reply so the bot can respond normally.
//
// A template cannot carry the report itself: Meta only allows free-form
// content, including documents, once the *user* has made contact. So the
// template's job is to prompt that contact, after which the existing /rekap
// command delivers the report.
func (c *Client) Reengage(ctx context.Context, chatID, reason string) error {
	waID, err := messaging.ParseCloudChatID(chatID)
	if err != nil {
		return err
	}
	if c.cfg.ReengagementTemplate == "" {
		return fmt.Errorf("no re-engagement template configured (%s)", reason)
	}
	return c.postMessage(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                waID,
		"type":              "template",
		"template": map[string]any{
			"name":     c.cfg.ReengagementTemplate,
			"language": map[string]any{"code": c.cfg.TemplateLanguage},
		},
	})
}

func (c *Client) postMessage(ctx context.Context, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint(c.cfg.PhoneNumberID+"/messages"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()
	_, err = decode(resp)
	return err
}

// uploadMedia stores a file against the sending number and returns its media
// ID, valid for 30 days.
func (c *Client) uploadMedia(ctx context.Context, data []byte, filename, mimeType string) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("messaging_product", "whatsapp"); err != nil {
		return "", err
	}
	if err := w.WriteField("type", mimeType); err != nil {
		return "", err
	}

	// The part needs an explicit Content-Type; CreateFormFile would default
	// it to application/octet-stream and Meta rejects the upload.
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", mimeType)
	part, err := w.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint(c.cfg.PhoneNumberID+"/media"), &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := decode(resp)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode media id: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("upload returned no media id")
	}
	return out.ID, nil
}

// DownloadMedia fetches an inbound attachment: resolve the media ID to a
// short-lived URL, then fetch it with the same bearer token.
func (c *Client) DownloadMedia(ctx context.Context, mediaID string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(mediaID), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	raw, err := decode(resp)
	if err != nil {
		return nil, "", err
	}
	var meta struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, "", fmt.Errorf("decode media metadata: %w", err)
	}
	if meta.URL == "" {
		return nil, "", fmt.Errorf("no media url for %s", mediaID)
	}
	if meta.FileSize > maxMediaBytes {
		return nil, "", fmt.Errorf("media is %d bytes, over the %d limit", meta.FileSize, maxMediaBytes)
	}

	// The lookaside URL still requires the access token.
	fetch, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return nil, "", err
	}
	fetch.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)

	mediaResp, err := c.http.Do(fetch)
	if err != nil {
		return nil, "", err
	}
	defer mediaResp.Body.Close()
	if mediaResp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetch media: http %d", mediaResp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(mediaResp.Body, maxMediaBytes))
	if err != nil {
		return nil, "", err
	}
	return data, meta.MimeType, nil
}

// graphError is the Graph API's error envelope.
type graphError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    int    `json:"code"`
	Details string `json:"error_data.details"`
}

// decode reads a Graph API response, translating documented failures into
// typed errors.
func decode(resp *http.Response) (json.RawMessage, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		var env struct {
			Error graphError `json:"error"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if env.Error.Code == reengagementErrorCode {
			return nil, fmt.Errorf("%w: %s", ErrOutsideWindow, env.Error.Message)
		}
		return nil, fmt.Errorf("graph error %d: %s", env.Error.Code, env.Error.Message)
	}
	return body, nil
}
