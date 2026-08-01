package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/go-sql-driver/mysql"
	"github.com/mdp/qrterminal/v3"
	_ "modernc.org/sqlite"
	"github.com/nurfaizh/keuanganku/config"
	"github.com/nurfaizh/keuanganku/internal/handler"
	"github.com/nurfaizh/keuanganku/internal/scheduler"
	"github.com/nurfaizh/keuanganku/internal/userstore"
	"github.com/nurfaizh/keuanganku/internal/wasend"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

func main() {
	cfg := config.Load()

	// Init MySQL
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("Open MySQL: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("Connect MySQL: %v", err)
	}

	// Init user store
	users, err := userstore.New(db)
	if err != nil {
		log.Fatalf("Init user store: %v", err)
	}
	if err := users.MigrateFromJSON("users.json"); err != nil {
		log.Fatalf("Migrate legacy users.json: %v", err)
	}
	log.Println("User store loaded ✓")

	// Init message handler
	msgHandler := handler.New(users, cfg.GoogleCredentials)

	// Init WhatsApp
	dbLog := waLog.Stdout("DB", "WARN", true)
	ctx := context.Background()
	container, err := sqlstore.New(ctx, "sqlite", "file:whatsmeow.db?_foreign_keys=on", dbLog)
	if err != nil {
		log.Fatalf("Init sqlstore: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		log.Fatalf("Get device: %v", err)
	}

	clientLog := waLog.Stdout("Client", "INFO", true)
	waClient := whatsmeow.NewClient(deviceStore, clientLog)

	waClient.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			msg := v.Message.GetConversation()
			if msg == "" {
				msg = v.Message.GetExtendedTextMessage().GetText()
			}
			if msg == "" {
				return
			}
			// Ignore bot's own replies, only allow /commands from self
			if v.Info.IsFromMe && !strings.HasPrefix(msg, "/") {
				return
			}

			chatJID := v.Info.Chat.String()
			log.Printf("Message in %s from %s: %s", chatJID, v.Info.Sender.User, msg)

			reply := msgHandler.Handle(context.Background(), chatJID, msg)
			switch {
			case reply.Document != nil:
				if err := wasend.SendDocument(context.Background(), waClient, v.Info.Chat, reply.Document, reply.Filename, reply.Caption); err != nil {
					log.Printf("send document: %v", err)
				}
			case reply.Text != "":
				waClient.SendMessage(context.Background(), v.Info.Chat, &waE2E.Message{
					Conversation: proto.String(reply.Text),
				})
			}
		}
	})

	// Connect / show QR (retry until scanned)
	if waClient.Store.ID == nil {
		for {
			qrChan, _ := waClient.GetQRChannel(ctx)
			if err := waClient.Connect(); err != nil {
				log.Fatalf("Connect WhatsApp: %v", err)
			}
			connected := false
			for evt := range qrChan {
				switch evt.Event {
				case "code":
					log.Println("Scan QR code with WhatsApp → Settings → Linked Devices → Link a Device")
					printQR(evt.Code)
				case "success":
					log.Println("QR scanned! Finishing connection, please wait...")
					connected = true
				case "timeout":
					log.Println("QR timed out, refreshing...")
				default:
					log.Printf("QR event: %s", evt.Event)
				}
			}
			if connected || waClient.Store.ID != nil {
				log.Println("WhatsApp connected ✓")
				break
			}
			log.Println("QR expired, generating new code...")
			waClient.Disconnect()
		}
	} else {
		if err := waClient.Connect(); err != nil {
			log.Fatalf("Connect WhatsApp: %v", err)
		}
		log.Println("WhatsApp connected ✓")
	}

	// Auto-send a monthly PDF report to every registered chat
	go scheduler.New(users, cfg.GoogleCredentials, waClient).Run(ctx)

	// Wait for signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	waClient.Disconnect()
	log.Println("Bot stopped.")
}

func printQR(code string) {
	qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
}
