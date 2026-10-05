// Command smoke exercises the Gemini intent parser end to end, without
// needing WhatsApp, MySQL or Google Sheets. It reads GEMINI_API_KEY from .env
// (or the environment), parses a handful of Indonesian messages against a
// fake wallet list, and prints the resulting Intent and the slash command it
// would run.
//
// Usage, from the repo root:
//
//	go run ./cmd/smoke
//	go run ./cmd/smoke "bayar listrik 300rb bca"
//	go run ./cmd/smoke -image path/to/receipt.jpg
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/nurfaizh/keuanganku/config"
	"github.com/nurfaizh/keuanganku/internal/ai"
)

// Stand-in for a real user's wallets, including a multi-word one.
var wallets = []string{"BCA", "GoPay", "Jago Kantong Belanja"}

var defaultMessages = []string{
	"beli nasi goreng 25rb pakai gopay",
	"bayar listrik 300rb dari bca",
	"gajian 5jt masuk bca",
	"pindah 500rb dari bca ke gopay",
	"belanja bulanan 250rb di jago kantong belanja",
	"saldo berapa",
	"rekap bulan lalu dong pdf",
	"eh salah, batalin",
	"tambah dompet Jenius",
	"halo apa kabar",      // should stay silent
	"bayar 500rb",         // ambiguous: no wallet -> should ask
	"beli kopi pakai ovo", // unknown wallet -> should ask
}

func main() {
	imagePath := flag.String("image", "", "optional receipt image to parse instead of text")
	// The free tier allows only a handful of requests per minute, so pace the
	// run rather than burning the quota on 429s.
	delay := flag.Duration("delay", 13*time.Second, "pause between messages")
	flag.Parse()

	_ = godotenv.Load()

	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		log.Fatal("GEMINI_API_KEY is not set (put it in .env at the repo root)")
	}
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = config.DefaultGeminiModel
	}

	ctx := context.Background()
	parser, err := ai.New(ctx, key, model)
	if err != nil {
		log.Fatalf("init parser: %v", err)
	}
	fmt.Printf("model: %s\nwallets: %v\n\n", model, wallets)

	if *imagePath != "" {
		// Any trailing words become the photo's caption.
		runImage(ctx, parser, *imagePath, strings.Join(flag.Args(), " "))
		return
	}

	messages := defaultMessages
	if args := flag.Args(); len(args) > 0 {
		messages = args
	}
	for i, m := range messages {
		if i > 0 {
			time.Sleep(*delay)
		}
		run(ctx, parser, ai.Request{Text: m, Wallets: wallets, Now: time.Now()}, m)
	}
}

func runImage(ctx context.Context, parser *ai.Parser, path, caption string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read image: %v", err)
	}
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	label := fmt.Sprintf("[image %s, %d bytes]", mimeType, len(data))
	if caption != "" {
		label += fmt.Sprintf(" caption=%q", caption)
	}
	run(ctx, parser, ai.Request{
		Text: caption, Image: data, ImageMIME: mimeType,
		Wallets: wallets, Now: time.Now(),
	}, label)
}

func run(ctx context.Context, parser *ai.Parser, req ai.Request, label string) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	start := time.Now()
	intent, err := parser.Parse(ctx, req)
	elapsed := time.Since(start).Round(time.Millisecond)

	fmt.Printf("── %s\n", label)
	if err != nil {
		fmt.Printf("   ERROR: %v\n\n", err)
		return
	}

	fmt.Printf("   intent : %+v\n", intent)
	if cmd, ok := intent.Command(); ok {
		fmt.Printf("   command: %s\n", cmd)
		if intent.Mutates() {
			fmt.Printf("   (mutating — reply would carry the /batal hint)\n")
		}
	} else if intent.Reply != "" {
		fmt.Printf("   asks   : %s\n", intent.Reply)
	} else {
		fmt.Printf("   silent (no command, no reply)\n")
	}
	fmt.Printf("   took   : %s\n\n", elapsed)
}
