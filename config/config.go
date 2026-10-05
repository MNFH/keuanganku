package config

import (
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// DefaultGeminiModel is the model used for natural-language and receipt
// parsing when GEMINI_MODEL is unset.
//
// Flash-Lite is chosen for cost: intent extraction against a fixed schema is
// an easy task, and this runs on every non-command message. The cheaper
// 2.5-flash-lite is deliberately avoided — that family is access-restricted
// and being withdrawn. Model IDs retire on a schedule, which is why this is
// overridable by environment without a rebuild.
const DefaultGeminiModel = "gemini-3.1-flash-lite"

type Config struct {
	GoogleCredentials string
	MySQLDSN          string

	// GeminiAPIKey is optional. When empty the bot still runs, but only
	// responds to slash commands — free-form messages and receipt photos are
	// ignored instead of being parsed.
	GeminiAPIKey string
	GeminiModel  string

	// TelegramBotToken is optional, from @BotFather. When set, the bot serves
	// Telegram alongside WhatsApp.
	TelegramBotToken string

	// DisableWhatsApp skips linking a WhatsApp account via whatsmeow (the QR
	// flow). Set this when running Telegram-only, or when using the official
	// Cloud API below instead of the linked-device transport.
	DisableWhatsApp bool

	// Official WhatsApp Business Cloud API. All of Token, PhoneNumberID,
	// VerifyToken and AppSecret are required together; the transport stays
	// off unless all four are set.
	CloudToken         string
	CloudPhoneNumberID string
	CloudVerifyToken   string
	CloudAppSecret     string
	CloudAPIVersion    string
	// CloudTemplate is an approved Utility template used to prompt a user
	// whose 24-hour window has closed (see internal/whatsappcloud).
	CloudTemplate     string
	CloudTemplateLang string
	// WebhookAddr is the listen address for the Cloud API webhook server.
	WebhookAddr string
	// WebhookPath is the URL path Meta posts to.
	WebhookPath string

	// MonthlyReport turns on the scheduled monthly PDF. It is opt-in because
	// it is the only thing the bot sends unprompted: on the WhatsApp Cloud
	// API that means a billable template conversation outside the free
	// 24-hour service window. Users can still pull a report any time with
	// /rekap, which is free because it answers their own message.
	MonthlyReport bool
}

// AIEnabled reports whether natural-language parsing is configured.
func (c *Config) AIEnabled() bool { return c.GeminiAPIKey != "" }

// TelegramEnabled reports whether the Telegram transport is configured.
func (c *Config) TelegramEnabled() bool { return c.TelegramBotToken != "" }

// WhatsAppEnabled reports whether the whatsmeow (linked-device) transport
// should start.
func (c *Config) WhatsAppEnabled() bool { return !c.DisableWhatsApp }

// CloudEnabled reports whether the official WhatsApp Cloud API transport is
// fully configured. It deliberately requires the app secret: without it the
// webhook cannot verify Meta's signature, and an unauthenticated endpoint
// would let anyone write transactions into a user's ledger.
func (c *Config) CloudEnabled() bool {
	return c.CloudToken != "" && c.CloudPhoneNumberID != "" &&
		c.CloudVerifyToken != "" && c.CloudAppSecret != ""
}

// CloudPartiallyConfigured reports a half-filled Cloud API setup, so startup
// can warn instead of silently running without it.
func (c *Config) CloudPartiallyConfigured() bool {
	any := c.CloudToken != "" || c.CloudPhoneNumberID != "" ||
		c.CloudVerifyToken != "" || c.CloudAppSecret != ""
	return any && !c.CloudEnabled()
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	return &Config{
		GoogleCredentials: getOrDefault("GOOGLE_CREDENTIALS_FILE", "credentials.json"),
		MySQLDSN:          mustGet("MYSQL_DSN"),
		GeminiAPIKey:      os.Getenv("GEMINI_API_KEY"),
		GeminiModel:       getOrDefault("GEMINI_MODEL", DefaultGeminiModel),
		TelegramBotToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
		DisableWhatsApp:   isTrue(os.Getenv("DISABLE_WHATSAPP")),

		CloudToken:         os.Getenv("WHATSAPP_CLOUD_TOKEN"),
		CloudPhoneNumberID: os.Getenv("WHATSAPP_PHONE_NUMBER_ID"),
		CloudVerifyToken:   os.Getenv("WHATSAPP_VERIFY_TOKEN"),
		CloudAppSecret:     os.Getenv("WHATSAPP_APP_SECRET"),
		CloudAPIVersion:    os.Getenv("WHATSAPP_API_VERSION"),
		CloudTemplate:      os.Getenv("WHATSAPP_REENGAGE_TEMPLATE"),
		CloudTemplateLang:  os.Getenv("WHATSAPP_TEMPLATE_LANG"),
		WebhookAddr:        getOrDefault("WEBHOOK_ADDR", ":8080"),
		WebhookPath:        getOrDefault("WEBHOOK_PATH", "/webhook"),

		MonthlyReport: isTrue(os.Getenv("ENABLE_MONTHLY_REPORT")),
	}
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

func mustGet(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("Missing required environment variable: %s", key)
	}
	return v
}

func getOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
