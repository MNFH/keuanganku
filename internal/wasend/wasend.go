// Package wasend sends messages and media through a whatsmeow client, shared
// by the reactive message handler and the monthly report scheduler.
//
// Sender adapts it to messaging.Sender, so callers that work across chat
// platforms (the scheduler, most importantly) need not know they are talking
// to WhatsApp.
package wasend

import (
	"context"
	"fmt"

	"github.com/nurfaizh/keuanganku/internal/messaging"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Sender is the WhatsApp implementation of messaging.Sender. Chat IDs are
// whatsmeow JID strings, as stored in the users table.
type Sender struct {
	Client *whatsmeow.Client
}

func (s Sender) SendText(ctx context.Context, chatID, text string) error {
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return fmt.Errorf("parse jid %q: %w", chatID, err)
	}
	// WhatsApp allows far longer messages than the other platforms; split
	// only past maxTextLen, as a safety net.
	for _, chunk := range messaging.SplitText(text, maxTextLen) {
		if _, err := s.Client.SendMessage(ctx, jid, &waE2E.Message{
			Conversation: proto.String(chunk),
		}); err != nil {
			return fmt.Errorf("send message: %w", err)
		}
	}
	return nil
}

// maxTextLen is well under WhatsApp's own message length limit.
const maxTextLen = 60000

func (s Sender) SendDocument(ctx context.Context, chatID string, data []byte, filename, caption string) error {
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return fmt.Errorf("parse jid %q: %w", chatID, err)
	}
	return SendDocument(ctx, s.Client, jid, data, filename, caption)
}

// SendDocument uploads data to WhatsApp's servers and sends it to `to` as a
// document attachment named filename, with caption as the message caption.
func SendDocument(ctx context.Context, client *whatsmeow.Client, to types.JID, data []byte, filename, caption string) error {
	uploaded, err := client.Upload(ctx, data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("upload document: %w", err)
	}

	msg := &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			Mimetype:      proto.String("application/pdf"),
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uploaded.FileLength),
			FileName:      proto.String(filename),
			Caption:       proto.String(caption),
		},
	}

	if _, err := client.SendMessage(ctx, to, msg); err != nil {
		return fmt.Errorf("send document: %w", err)
	}
	return nil
}
