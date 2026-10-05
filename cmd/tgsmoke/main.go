// Command tgsmoke exercises the Telegram transport end to end without
// needing MySQL, Google Sheets or the message handler.
//
// It confirms the token with getMe, then long-polls. Every message you send
// the bot is echoed back through the real send path — including the
// WhatsApp-markup-to-HTML conversion — and photos are downloaded and their
// size reported, which is the same code the receipt parser is fed from.
//
// Usage, from the repo root:
//
//	go run ./cmd/tgsmoke            # poll until Ctrl-C
//	go run ./cmd/tgsmoke -once      # exit after the first message
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/nurfaizh/keuanganku/internal/messaging"
	"github.com/nurfaizh/keuanganku/internal/telegram"
)

func main() {
	once := flag.Bool("once", false, "exit after handling one message")
	flag.Parse()

	_ = godotenv.Load()

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is not set (put it in .env at the repo root)")
	}

	bot := telegram.New(token)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	checkCtx, checkCancel := context.WithTimeout(ctx, 30*time.Second)
	username, err := bot.Me(checkCtx)
	checkCancel()
	if err != nil {
		log.Fatalf("getMe failed — is the token right? %v", err)
	}
	fmt.Printf("connected as @%s ✓\n\nSend the bot a message (or a photo). Ctrl-C to stop.\n\n", username)

	// Ctrl-C stops the poll loop cleanly.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nstopping...")
		cancel()
	}()

	done := make(chan struct{})
	bot.Run(ctx, func(ctx context.Context, m telegram.Message) {
		chatID := messaging.TelegramChatID(m.ChatID)
		fmt.Printf("── from %s in %s\n", m.From, chatID)
		if len(m.Photo) > 0 {
			fmt.Printf("   photo  : %d bytes (%s)\n", len(m.Photo), m.PhotoMIME)
		}
		fmt.Printf("   text   : %q\n", m.Text)

		// Echo through the real send path, exercising ToHTML on the way.
		reply := fmt.Sprintf("✅ *Diterima!*\n\nPesan: _%s_", m.Text)
		if len(m.Photo) > 0 {
			reply = fmt.Sprintf("📸 *Foto diterima!*\n\n%d bytes, tipe %s\nCaption: _%s_",
				len(m.Photo), m.PhotoMIME, m.Text)
		}

		sendCtx, sendCancel := context.WithTimeout(ctx, 30*time.Second)
		defer sendCancel()
		if err := bot.SendText(sendCtx, chatID, reply); err != nil {
			fmt.Printf("   SEND ERROR: %v\n\n", err)
			return
		}
		fmt.Printf("   replied ✓\n\n")

		if *once {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	}, log.Printf)

	if *once {
		select {
		case <-done:
		default:
		}
	}
}
