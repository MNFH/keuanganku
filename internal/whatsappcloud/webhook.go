package whatsappcloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Message is one inbound WhatsApp message, normalised for the handler.
type Message struct {
	// WaID is the sender's phone number in international format, digits only.
	WaID string
	// From is a display name, for logging only.
	From string
	Text string
	// Image is a downloaded attachment, nil when the message had none.
	Image     []byte
	ImageMIME string
}

// Handler processes one inbound message.
type Handler func(ctx context.Context, msg Message)

// Webhook serves Meta's callback endpoint: a GET handshake to verify
// ownership, and POSTs carrying message events.
type Webhook struct {
	client *Client
	// verifyToken is the arbitrary string also entered in the Meta dashboard,
	// echoed back during the GET handshake.
	verifyToken string
	// appSecret signs every POST. Without it the endpoint is unauthenticated
	// and anyone who learns the URL can inject messages — which for this bot
	// means writing transactions into someone's ledger.
	appSecret string
	handle    Handler
	logf      func(string, ...any)
}

func NewWebhook(client *Client, verifyToken, appSecret string, handle Handler, logf func(string, ...any)) *Webhook {
	return &Webhook{
		client:      client,
		verifyToken: verifyToken,
		appSecret:   appSecret,
		handle:      handle,
		logf:        logf,
	}
}

func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.verify(rw, r)
	case http.MethodPost:
		w.receive(rw, r)
	default:
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// verify answers Meta's subscription handshake by echoing hub.challenge, but
// only when hub.verify_token matches.
func (w *Webhook) verify(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("hub.mode") != "subscribe" ||
		subtle.ConstantTimeCompare([]byte(q.Get("hub.verify_token")), []byte(w.verifyToken)) != 1 {
		w.logf("whatsappcloud: webhook verification rejected")
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}
	rw.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(rw, q.Get("hub.challenge"))
	w.logf("whatsappcloud: webhook verified ✓")
}

func (w *Webhook) receive(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}

	if !w.validSignature(r.Header.Get("X-Hub-Signature-256"), body) {
		w.logf("whatsappcloud: rejected webhook with bad signature")
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}

	// Acknowledge immediately. Meta retries anything not answered quickly,
	// and the work below (media download, an LLM call, Sheets writes) takes
	// far longer than its patience allows — a retry would double-record the
	// transaction.
	rw.WriteHeader(http.StatusOK)

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.logf("whatsappcloud: decode webhook: %v", err)
		return
	}

	for _, msg := range payload.messages(w.logf) {
		go w.dispatch(msg)
	}
}

// dispatch runs one message on its own goroutine with a detached context, so
// the work outlives the webhook response we have already sent.
func (w *Webhook) dispatch(in inboundMessage) {
	ctx := context.Background()

	out := Message{WaID: in.From, From: in.ContactName, Text: in.body()}

	if in.Image != nil && in.Image.ID != "" {
		data, mime, err := w.client.DownloadMedia(ctx, in.Image.ID)
		if err != nil {
			w.logf("whatsappcloud: download media: %v", err)
			return
		}
		out.Image = data
		out.ImageMIME = mime
		if out.Text == "" {
			out.Text = in.Image.Caption
		}
	}

	if out.Text == "" && out.Image == nil {
		return
	}
	w.handle(ctx, out)
}

// validSignature checks Meta's HMAC-SHA256 over the raw body.
func (w *Webhook) validSignature(header string, body []byte) bool {
	if w.appSecret == "" {
		// Refuse rather than accept unsigned traffic: an unauthenticated
		// endpoint here lets anyone write to a user's ledger.
		return false
	}
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	want, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(w.appSecret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// Wire types. Only the fields this bot reads are declared.

type webhookPayload struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Contacts []struct {
					WaID    string `json:"wa_id"`
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
				} `json:"contacts"`
				Messages []inboundMessage `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type inboundMessage struct {
	From string `json:"from"`
	Type string `json:"type"`
	Text struct {
		Body string `json:"body"`
	} `json:"text"`
	Image *struct {
		ID      string `json:"id"`
		Caption string `json:"caption"`
	} `json:"image"`

	// ContactName is filled in from the sibling contacts array.
	ContactName string `json:"-"`
}

func (m inboundMessage) body() string {
	if m.Text.Body != "" {
		return m.Text.Body
	}
	if m.Image != nil {
		return m.Image.Caption
	}
	return ""
}

// messages flattens the nested payload and attaches each sender's display
// name. Statuses (delivered/read receipts) carry no messages array and are
// skipped naturally.
func (p webhookPayload) messages(logf func(string, ...any)) []inboundMessage {
	var out []inboundMessage
	for _, entry := range p.Entry {
		for _, change := range entry.Changes {
			names := make(map[string]string, len(change.Value.Contacts))
			for _, c := range change.Value.Contacts {
				names[c.WaID] = c.Profile.Name
			}
			for _, m := range change.Value.Messages {
				switch m.Type {
				case "text", "image":
					m.ContactName = names[m.From]
					out = append(out, m)
				default:
					// Audio, location, stickers, reactions: nothing to do.
					logf("whatsappcloud: ignoring %q message from %s", m.Type, m.From)
				}
			}
		}
	}
	return out
}
