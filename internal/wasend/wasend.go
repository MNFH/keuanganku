// Package wasend sends media (documents) through a whatsmeow client, shared
// by the reactive message handler and the monthly report scheduler.
package wasend

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

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
