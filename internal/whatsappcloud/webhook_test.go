package whatsappcloud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func quiet(string, ...any) {}

// The webhook is a public endpoint. If signature checking is wrong, anyone
// who learns the URL can post messages that the bot will act on — writing
// transactions into a user's ledger.
func TestValidSignature(t *testing.T) {
	const secret = "app-secret"
	body := []byte(`{"object":"whatsapp_business_account"}`)
	w := NewWebhook(nil, "verify", secret, nil, quiet)

	t.Run("accepts a correct signature", func(t *testing.T) {
		if !w.validSignature(sign(secret, body), body) {
			t.Error("valid signature rejected")
		}
	})

	rejects := []struct {
		name   string
		header string
		body   []byte
	}{
		{"wrong secret", sign("not-the-secret", body), body},
		{"tampered body", sign(secret, body), []byte(`{"object":"evil"}`)},
		{"missing header", "", body},
		{"missing prefix", strings.TrimPrefix(sign(secret, body), "sha256="), body},
		{"not hex", "sha256=zzzz", body},
		{"empty digest", "sha256=", body},
	}
	for _, tt := range rejects {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			if w.validSignature(tt.header, tt.body) {
				t.Error("invalid signature accepted")
			}
		})
	}

	// With no secret configured the endpoint would be unauthenticated, so it
	// must refuse everything rather than fail open.
	t.Run("refuses everything when no secret is set", func(t *testing.T) {
		open := NewWebhook(nil, "verify", "", nil, quiet)
		if open.validSignature(sign("", body), body) {
			t.Error("unsigned webhook accepted with no app secret configured")
		}
	})
}

func TestVerifyHandshake(t *testing.T) {
	w := NewWebhook(nil, "my-token", "secret", nil, quiet)

	t.Run("echoes the challenge for the right token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet,
			"/webhook?hub.mode=subscribe&hub.verify_token=my-token&hub.challenge=12345", nil)
		rec := httptest.NewRecorder()
		w.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Body.String(); got != "12345" {
			t.Errorf("body = %q, want the challenge echoed", got)
		}
	})

	for _, tt := range []struct{ name, query string }{
		{"wrong token", "hub.mode=subscribe&hub.verify_token=guess&hub.challenge=12345"},
		{"wrong mode", "hub.mode=unsubscribe&hub.verify_token=my-token&hub.challenge=12345"},
		{"no token", "hub.mode=subscribe&hub.challenge=12345"},
	} {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/webhook?"+tt.query, nil)
			rec := httptest.NewRecorder()
			w.ServeHTTP(rec, r)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestPayloadMessages(t *testing.T) {
	const body = `{
	  "entry": [{
	    "changes": [{
	      "value": {
	        "contacts": [{"wa_id": "628123", "profile": {"name": "Hilma"}}],
	        "messages": [
	          {"from": "628123", "type": "text", "text": {"body": "beli bakso 20rb"}},
	          {"from": "628123", "type": "image", "image": {"id": "MED1", "caption": "pakai gopay"}},
	          {"from": "628123", "type": "audio"}
	        ]
	      }
	    }]
	  }]
	}`

	var p webhookPayload
	if err := decodeJSON(body, &p); err != nil {
		t.Fatal(err)
	}

	msgs := p.messages(quiet)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (audio should be skipped)", len(msgs))
	}

	if msgs[0].body() != "beli bakso 20rb" {
		t.Errorf("text body = %q", msgs[0].body())
	}
	if msgs[0].ContactName != "Hilma" {
		t.Errorf("contact name = %q, want it joined from the contacts array", msgs[0].ContactName)
	}
	// An image's caption stands in for the message text.
	if msgs[1].body() != "pakai gopay" {
		t.Errorf("image caption = %q", msgs[1].body())
	}
	if msgs[1].Image == nil || msgs[1].Image.ID != "MED1" {
		t.Errorf("image id not parsed: %+v", msgs[1].Image)
	}
}

// A status callback (delivery receipt) carries no messages and must not be
// mistaken for one.
func TestPayloadIgnoresStatuses(t *testing.T) {
	const body = `{"entry":[{"changes":[{"value":{"statuses":[{"status":"delivered"}]}}]}]}`
	var p webhookPayload
	if err := decodeJSON(body, &p); err != nil {
		t.Fatal(err)
	}
	if msgs := p.messages(quiet); len(msgs) != 0 {
		t.Errorf("got %d messages from a status callback, want 0", len(msgs))
	}
}

// decodeJSON is a small helper so the tests read as payload fixtures.
func decodeJSON(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
