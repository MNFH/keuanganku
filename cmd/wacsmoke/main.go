// Command wacsmoke exercises the WhatsApp Cloud API transport end to end
// without needing MySQL, Google Sheets or the message handler.
//
// It starts the webhook server, waits for Meta to deliver messages, and
// echoes each one back through the real Graph API send path. Photos are
// downloaded and their size reported — the same code the receipt parser is
// fed from.
//
// Meta only delivers to a public HTTPS URL, so run a tunnel alongside it:
//
//	cloudflared tunnel --url http://localhost:8080
//	ngrok http 8080
//
// then point the app's callback URL at https://<tunnel>/webhook.
//
// Usage, from the repo root:
//
//	go run ./cmd/wacsmoke
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/nurfaizh/keuanganku/config"
	"github.com/nurfaizh/keuanganku/internal/messaging"
	"github.com/nurfaizh/keuanganku/internal/whatsappcloud"
)

func main() {
	_ = godotenv.Load()
	cfg := loadOrDie()

	client := whatsappcloud.New(whatsappcloud.Config{
		AccessToken:          cfg.CloudToken,
		PhoneNumberID:        cfg.CloudPhoneNumberID,
		APIVersion:           cfg.CloudAPIVersion,
		ReengagementTemplate: cfg.CloudTemplate,
		TemplateLanguage:     cfg.CloudTemplateLang,
	})

	hook := whatsappcloud.NewWebhook(client, cfg.CloudVerifyToken, cfg.CloudAppSecret,
		func(ctx context.Context, m whatsappcloud.Message) {
			chatID := messaging.CloudChatID(m.WaID)
			fmt.Printf("── from %s (%s)\n", m.From, chatID)
			if len(m.Image) > 0 {
				fmt.Printf("   image  : %d bytes (%s)\n", len(m.Image), m.ImageMIME)
			}
			fmt.Printf("   text   : %q\n", m.Text)

			reply := fmt.Sprintf("✅ *Diterima!*\n\nPesan: _%s_", m.Text)
			if len(m.Image) > 0 {
				reply = fmt.Sprintf("📸 *Foto diterima!*\n\n%d bytes, tipe %s\nCaption: _%s_",
					len(m.Image), m.ImageMIME, m.Text)
			}

			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := client.SendText(sendCtx, chatID, reply); err != nil {
				if errors.Is(err, messaging.ErrOutsideWindow) {
					fmt.Printf("   SEND BLOCKED: outside the 24h window — this should not\n" +
						"   happen right after an inbound message; check the phone number ID\n\n")
					return
				}
				fmt.Printf("   SEND ERROR: %v\n\n", err)
				return
			}
			fmt.Printf("   replied ✓\n\n")
		}, log.Printf)

	mux := http.NewServeMux()
	mux.Handle(cfg.WebhookPath, hook)
	srv := &http.Server{
		Addr:              cfg.WebhookAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("webhook server: %v", err)
		}
	}()

	fmt.Printf("webhook listening on %s%s\n\n", cfg.WebhookAddr, cfg.WebhookPath)
	fmt.Println("Next, in another terminal:")
	fmt.Printf("  cloudflared tunnel --url http://localhost%s\n\n", cfg.WebhookAddr)
	fmt.Println("Then in the Meta dashboard (WhatsApp → Configuration):")
	fmt.Printf("  Callback URL : https://<your-tunnel>%s\n", cfg.WebhookPath)
	fmt.Printf("  Verify token : %s\n", cfg.CloudVerifyToken)
	fmt.Println("  Subscribe to the \"messages\" field.")
	fmt.Println("\nThen message the business number. Ctrl-C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	fmt.Println("\nstopping...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// loadOrDie reports every missing credential at once, rather than failing on
// them one at a time.
func loadOrDie() *config.Config {
	cfg := &config.Config{
		CloudToken:         os.Getenv("WHATSAPP_CLOUD_TOKEN"),
		CloudPhoneNumberID: os.Getenv("WHATSAPP_PHONE_NUMBER_ID"),
		CloudVerifyToken:   os.Getenv("WHATSAPP_VERIFY_TOKEN"),
		CloudAppSecret:     os.Getenv("WHATSAPP_APP_SECRET"),
		CloudAPIVersion:    os.Getenv("WHATSAPP_API_VERSION"),
		CloudTemplate:      os.Getenv("WHATSAPP_REENGAGE_TEMPLATE"),
		CloudTemplateLang:  os.Getenv("WHATSAPP_TEMPLATE_LANG"),
		WebhookAddr:        envOr("WEBHOOK_ADDR", ":8080"),
		WebhookPath:        envOr("WEBHOOK_PATH", "/webhook"),
	}

	var missing []string
	for _, v := range []struct {
		name, value string
	}{
		{"WHATSAPP_CLOUD_TOKEN", cfg.CloudToken},
		{"WHATSAPP_PHONE_NUMBER_ID", cfg.CloudPhoneNumberID},
		{"WHATSAPP_VERIFY_TOKEN", cfg.CloudVerifyToken},
		{"WHATSAPP_APP_SECRET", cfg.CloudAppSecret},
	} {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		log.Fatalf("missing in .env: %v\n\nGet these from developers.facebook.com → your app →\n"+
			"WhatsApp → API Setup (token, phone number ID) and App Settings → Basic (app secret).\n"+
			"WHATSAPP_VERIFY_TOKEN is any string you invent.", missing)
	}
	return cfg
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
