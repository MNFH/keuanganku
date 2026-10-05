package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/mdp/qrterminal/v3"
	"github.com/nurfaizh/keuanganku/config"
	"github.com/nurfaizh/keuanganku/internal/ai"
	"github.com/nurfaizh/keuanganku/internal/handler"
	"github.com/nurfaizh/keuanganku/internal/messaging"
	"github.com/nurfaizh/keuanganku/internal/scheduler"
	"github.com/nurfaizh/keuanganku/internal/telegram"
	"github.com/nurfaizh/keuanganku/internal/userstore"
	"github.com/nurfaizh/keuanganku/internal/wasend"
	"github.com/nurfaizh/keuanganku/internal/whatsappcloud"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

// messageTimeout bounds the work done for a single inbound message —
// downloading media, the natural-language parse, and the Sheets calls behind
// the command it produces.
const messageTimeout = 60 * time.Second

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

	// Init natural-language parser. Optional: without a Gemini key the bot
	// still runs, but only answers slash commands.
	var parser handler.IntentParser
	if cfg.AIEnabled() {
		p, err := ai.New(context.Background(), cfg.GeminiAPIKey, cfg.GeminiModel)
		if err != nil {
			log.Fatalf("Init Gemini parser: %v", err)
		}
		parser = p
		log.Printf("Natural-language parsing enabled (%s) ✓", cfg.GeminiModel)
	} else {
		log.Println("GEMINI_API_KEY not set — slash commands only")
	}

	msgHandler := handler.New(users, cfg.GoogleCredentials, parser)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// router lets the scheduler reach a chat on whichever platform it
	// registered from, without knowing which that is.
	router := &messaging.Router{}

	var waClient *whatsmeow.Client
	if cfg.WhatsAppEnabled() {
		waClient = startWhatsApp(ctx, msgHandler, router)
	} else {
		log.Println("DISABLE_WHATSAPP set — not linking a WhatsApp account")
	}

	if cfg.TelegramEnabled() {
		bot := telegram.New(cfg.TelegramBotToken)
		router.Telegram = bot

		// Confirm the token up front. A rejected token is fatal — otherwise
		// the bot polls forever and silently never receives anything — but a
		// network blip is not, since the poll loop retries with backoff.
		checkCtx, checkCancel := context.WithTimeout(ctx, 30*time.Second)
		username, err := bot.Me(checkCtx)
		checkCancel()
		switch {
		case err == nil:
			log.Printf("Telegram transport started as @%s ✓", username)
		case isTelegramRejection(err):
			log.Fatalf("Telegram rejected the token: %v", err)
		default:
			log.Printf("Telegram: could not reach the API at startup (%v) — polling anyway", err)
		}

		go bot.Run(ctx, telegramHandler(msgHandler, bot), log.Printf)
	} else {
		log.Println("TELEGRAM_BOT_TOKEN not set — Telegram disabled")
	}

	var webhookServer *http.Server
	switch {
	case cfg.CloudEnabled():
		webhookServer = startCloud(ctx, cfg, msgHandler, router)
	case cfg.CloudPartiallyConfigured():
		// Fail loudly: a half-configured Cloud API silently serves nobody,
		// and the missing piece is often the app secret that authenticates
		// the webhook.
		log.Fatal("WhatsApp Cloud API is partially configured — all of " +
			"WHATSAPP_CLOUD_TOKEN, WHATSAPP_PHONE_NUMBER_ID, WHATSAPP_VERIFY_TOKEN " +
			"and WHATSAPP_APP_SECRET are required together")
	default:
		log.Println("WHATSAPP_CLOUD_TOKEN not set — official Cloud API disabled")
	}

	if router.WhatsApp == nil && router.Telegram == nil && router.Cloud == nil {
		log.Fatal("No chat transport configured: set TELEGRAM_BOT_TOKEN or the " +
			"WHATSAPP_CLOUD_* variables, or leave DISABLE_WHATSAPP unset")
	}

	// Auto-send a monthly PDF report to every registered chat. Opt-in: it is
	// the only unprompted message the bot sends, and on the WhatsApp Cloud
	// API that is a billable template conversation rather than a free reply.
	if cfg.MonthlyReport {
		msgHandler.MonthlyReports = true
		go scheduler.New(users, cfg.GoogleCredentials, router).Run(ctx)
		log.Println("Monthly report scheduler enabled ✓")
	} else {
		log.Println("ENABLE_MONTHLY_REPORT not set — monthly reports off; /rekap still works on demand")
	}

	// Wait for signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	cancel()

	if webhookServer != nil {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		if err := webhookServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("webhook shutdown: %v", err)
		}
		stop()
	}
	if waClient != nil {
		waClient.Disconnect()
	}
	log.Println("Bot stopped.")
}

// startCloud brings up the official WhatsApp Cloud API transport: a Graph API
// client for sending, and an HTTPS webhook for receiving.
//
// Meta delivers messages by POSTing to a public URL, so unlike the polling
// transports this one needs to be reachable from the internet — behind a
// reverse proxy with TLS, or a tunnel during development.
func startCloud(ctx context.Context, cfg *config.Config, msgHandler *handler.MessageHandler, router *messaging.Router) *http.Server {
	client := whatsappcloud.New(whatsappcloud.Config{
		AccessToken:          cfg.CloudToken,
		PhoneNumberID:        cfg.CloudPhoneNumberID,
		APIVersion:           cfg.CloudAPIVersion,
		ReengagementTemplate: cfg.CloudTemplate,
		TemplateLanguage:     cfg.CloudTemplateLang,
	})
	router.Cloud = client

	hook := whatsappcloud.NewWebhook(client, cfg.CloudVerifyToken, cfg.CloudAppSecret,
		cloudHandler(msgHandler, client), log.Printf)

	mux := http.NewServeMux()
	mux.Handle(cfg.WebhookPath, hook)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:              cfg.WebhookAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Webhook server: %v", err)
		}
	}()

	log.Printf("WhatsApp Cloud API webhook listening on %s%s ✓", cfg.WebhookAddr, cfg.WebhookPath)
	if cfg.CloudTemplate == "" {
		log.Println("WHATSAPP_REENGAGE_TEMPLATE not set — monthly reports will be " +
			"dropped for users whose 24-hour window has closed")
	}
	return srv
}

func cloudHandler(h *handler.MessageHandler, sender messaging.Sender) whatsappcloud.Handler {
	return func(ctx context.Context, m whatsappcloud.Message) {
		ctx, cancel := context.WithTimeout(ctx, messageTimeout)
		defer cancel()

		chatID := messaging.CloudChatID(m.WaID)
		if len(m.Image) > 0 {
			log.Printf("Cloud image in %s from %s (%s, %d bytes), caption: %q",
				chatID, m.From, m.ImageMIME, len(m.Image), m.Text)
		} else {
			log.Printf("Cloud message in %s from %s: %s", chatID, m.From, m.Text)
		}

		dispatch(ctx, h, sender, chatID, handler.Message{
			Text:      m.Text,
			Image:     m.Image,
			ImageMIME: m.ImageMIME,
		})
	}
}

// dispatch runs one message through the handler and delivers whatever it
// returns. Every transport funnels through here, so their behaviour cannot
// drift apart.
func dispatch(ctx context.Context, h *handler.MessageHandler, sender messaging.Sender, chatID string, msg handler.Message) {
	reply := h.Handle(ctx, chatID, msg)
	switch {
	case reply.Document != nil:
		if err := sender.SendDocument(ctx, chatID, reply.Document, reply.Filename, reply.Caption); err != nil {
			log.Printf("send document to %s: %v", chatID, err)
		}
	case reply.Text != "":
		if err := sender.SendText(ctx, chatID, reply.Text); err != nil {
			log.Printf("send message to %s: %v", chatID, err)
		}
	}
}

// isTelegramRejection reports whether Telegram itself refused the request (a
// bad token, say), as opposed to the request never arriving.
func isTelegramRejection(err error) bool {
	var apiErr *telegram.APIError
	return errors.As(err, &apiErr)
}

func telegramHandler(h *handler.MessageHandler, sender messaging.Sender) telegram.Handler {
	return func(ctx context.Context, m telegram.Message) {
		ctx, cancel := context.WithTimeout(ctx, messageTimeout)
		defer cancel()

		chatID := messaging.TelegramChatID(m.ChatID)
		if len(m.Photo) > 0 {
			log.Printf("Telegram image in %s from %s (%d bytes), caption: %q",
				chatID, m.From, len(m.Photo), m.Text)
		} else {
			log.Printf("Telegram message in %s from %s: %s", chatID, m.From, m.Text)
		}

		dispatch(ctx, h, sender, chatID, handler.Message{
			Text:      m.Text,
			Image:     m.Photo,
			ImageMIME: m.PhotoMIME,
		})
	}
}

// startWhatsApp links the WhatsApp account (prompting with a QR code on first
// run), registers the message handler, and installs the transport on router.
func startWhatsApp(ctx context.Context, msgHandler *handler.MessageHandler, router *messaging.Router) *whatsmeow.Client {
	dbLog := waLog.Stdout("DB", "WARN", true)
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
	sender := wasend.Sender{Client: waClient}
	router.WhatsApp = sender

	waClient.AddEventHandler(func(evt interface{}) {
		v, ok := evt.(*events.Message)
		if !ok {
			return
		}

		text := v.Message.GetConversation()
		if text == "" {
			text = v.Message.GetExtendedTextMessage().GetText()
		}

		// A photo arrives as an ImageMessage, with any caption of its own.
		img := v.Message.GetImageMessage()
		if img != nil && text == "" {
			text = img.GetCaption()
		}

		if text == "" && img == nil {
			return
		}

		// Ignore the bot's own replies. Messages from this account are only
		// acted on when they're explicit commands, since the bot's own
		// outgoing text also comes back through here and would otherwise be
		// re-parsed as user input.
		if v.Info.IsFromMe && !strings.HasPrefix(text, "/") {
			return
		}

		chatID := v.Info.Chat.String()

		// Natural-language parsing and Sheets calls are network round trips,
		// so bound them rather than letting one hang this handler.
		msgCtx, cancel := context.WithTimeout(ctx, messageTimeout)
		defer cancel()

		msg := handler.Message{Text: text}
		if img != nil {
			data, err := waClient.Download(msgCtx, img)
			if err != nil {
				log.Printf("download image in %s: %v", chatID, err)
				return
			}
			msg.Image = data
			msg.ImageMIME = img.GetMimetype()
			log.Printf("Image in %s from %s (%s, %d bytes), caption: %q",
				chatID, v.Info.Sender.User, msg.ImageMIME, len(data), text)
		} else {
			log.Printf("Message in %s from %s: %s", chatID, v.Info.Sender.User, text)
		}

		dispatch(msgCtx, msgHandler, sender, chatID, msg)
	})

	connectWhatsApp(ctx, waClient)
	return waClient
}

// connectWhatsApp connects, showing a QR code and retrying until it is
// scanned when no session is stored yet.
func connectWhatsApp(ctx context.Context, waClient *whatsmeow.Client) {
	if waClient.Store.ID != nil {
		if err := waClient.Connect(); err != nil {
			log.Fatalf("Connect WhatsApp: %v", err)
		}
		log.Println("WhatsApp connected ✓")
		return
	}

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
			return
		}
		log.Println("QR expired, generating new code...")
		waClient.Disconnect()
	}
}

func printQR(code string) {
	qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
}
