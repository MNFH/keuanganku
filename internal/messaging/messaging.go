// Package messaging decouples the bot's logic from the chat platform it runs
// on. The handler and the scheduler address a user by an opaque chat ID and
// hand off a reply; this package works out which transport that ID belongs to
// and delivers it.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Sender delivers messages on one chat platform.
type Sender interface {
	SendText(ctx context.Context, chatID, text string) error
	SendDocument(ctx context.Context, chatID string, data []byte, filename, caption string) error
}

// Chat IDs carry a prefix naming their platform, except the original
// WhatsApp-web transport: those are stored bare, as the whatsmeow JID string
// they have always been ("628…@s.whatsapp.net"). Prefixing only the newer
// platforms keeps every existing row in the users table valid, so each new
// transport has needed no migration.
const (
	TelegramPrefix = "tg:"
	// CloudPrefix marks a chat on the official WhatsApp Business Cloud API,
	// e.g. "wac:628123456789". It is distinct from the bare whatsmeow JID so
	// both WhatsApp transports can run side by side during a migration.
	CloudPrefix = "wac:"
)

// IsTelegram reports whether a chat ID belongs to Telegram.
func IsTelegram(chatID string) bool {
	return strings.HasPrefix(chatID, TelegramPrefix)
}

// IsCloud reports whether a chat ID belongs to the WhatsApp Cloud API.
func IsCloud(chatID string) bool {
	return strings.HasPrefix(chatID, CloudPrefix)
}

// CloudChatID builds the stored form of a Cloud API chat ID from a wa_id
// (the user's phone number in international format, digits only).
func CloudChatID(waID string) string { return CloudPrefix + waID }

// ParseCloudChatID recovers the wa_id from its stored form.
func ParseCloudChatID(chatID string) (string, error) {
	waID, ok := strings.CutPrefix(chatID, CloudPrefix)
	if !ok || waID == "" {
		return "", fmt.Errorf("not a whatsapp cloud chat id: %q", chatID)
	}
	return waID, nil
}

// ErrOutsideWindow reports that a transport refused an unsolicited message
// because the user has not been in contact recently enough. It lives here
// rather than in the transport so callers can react without importing a
// specific platform.
var ErrOutsideWindow = errors.New("outside the messaging window")

// Reengager is implemented by transports that cannot deliver an unsolicited
// message at any time, and need a way to prompt the user to make contact
// first. The WhatsApp Cloud API is the case this exists for: outside a
// 24-hour window opened by the user's own message, only pre-approved
// templates may be sent.
//
// Transports without that restriction simply don't implement it.
type Reengager interface {
	// Reengage nudges the user to reply, so a normal message can follow.
	Reengage(ctx context.Context, chatID, reason string) error
}

// TelegramChatID builds the stored form of a Telegram chat ID.
func TelegramChatID(id int64) string {
	return fmt.Sprintf("%s%d", TelegramPrefix, id)
}

// ParseTelegramChatID recovers the numeric Telegram chat ID from its stored
// form.
func ParseTelegramChatID(chatID string) (int64, error) {
	raw, ok := strings.CutPrefix(chatID, TelegramPrefix)
	if !ok {
		return 0, fmt.Errorf("not a telegram chat id: %q", chatID)
	}
	var id int64
	if _, err := fmt.Sscanf(raw, "%d", &id); err != nil {
		return 0, fmt.Errorf("parse telegram chat id %q: %w", chatID, err)
	}
	return id, nil
}

// Router picks the right Sender for a chat ID. Either transport may be nil
// when it isn't configured; sending to an unconfigured one is an error rather
// than a silent drop, so a half-configured deployment is noticed.
type Router struct {
	WhatsApp Sender // whatsmeow (linked device); handles bare JIDs
	Telegram Sender
	Cloud    Sender // official WhatsApp Business Cloud API
}

// For returns the Sender responsible for a chat ID.
func (r *Router) For(chatID string) (Sender, error) {
	switch {
	case IsTelegram(chatID):
		if r.Telegram == nil {
			return nil, fmt.Errorf("telegram transport not configured (chat %s)", chatID)
		}
		return r.Telegram, nil
	case IsCloud(chatID):
		if r.Cloud == nil {
			return nil, fmt.Errorf("whatsapp cloud transport not configured (chat %s)", chatID)
		}
		return r.Cloud, nil
	default:
		if r.WhatsApp == nil {
			return nil, fmt.Errorf("whatsapp transport not configured (chat %s)", chatID)
		}
		return r.WhatsApp, nil
	}
}

func (r *Router) SendText(ctx context.Context, chatID, text string) error {
	s, err := r.For(chatID)
	if err != nil {
		return err
	}
	return s.SendText(ctx, chatID, text)
}

func (r *Router) SendDocument(ctx context.Context, chatID string, data []byte, filename, caption string) error {
	s, err := r.For(chatID)
	if err != nil {
		return err
	}
	return s.SendDocument(ctx, chatID, data, filename, caption)
}

// Reengage asks the transport responsible for chatID to prompt the user for
// contact. It fails for transports that have no such mechanism, which is the
// correct outcome: they had no reason to refuse the message in the first
// place.
func (r *Router) Reengage(ctx context.Context, chatID, reason string) error {
	s, err := r.For(chatID)
	if err != nil {
		return err
	}
	re, ok := s.(Reengager)
	if !ok {
		return fmt.Errorf("transport for %s cannot re-engage", chatID)
	}
	return re.Reengage(ctx, chatID, reason)
}
