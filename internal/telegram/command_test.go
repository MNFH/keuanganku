package telegram

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeCommand(t *testing.T) {
	tests := []struct {
		text, username string
		want           string
		forUs          bool
	}{
		{"/dompet@AqsaWallet_bot list", "AqsaWallet_bot", "/dompet list", true},
		{"/help@aqsawallet_bot", "AqsaWallet_bot", "/help", true}, // usernames are case-insensitive
		{"/dompet list", "AqsaWallet_bot", "/dompet list", true},
		{"/help@OtherBot", "AqsaWallet_bot", "/help@OtherBot", false},
		{"/help@OtherBot", "", "/help", true}, // own name unknown: strip rather than drop
		{"jajan 50k @teman", "AqsaWallet_bot", "jajan 50k @teman", true},
		{"/keluar 50k jago makan bareng @teman", "AqsaWallet_bot", "/keluar 50k jago makan bareng @teman", true},
		{"/saldo@AqsaWallet_bot\nlagi", "AqsaWallet_bot", "/saldo\nlagi", true},
	}
	for _, tt := range tests {
		got, forUs := normalizeCommand(tt.text, tt.username)
		if got != tt.want || forUs != tt.forUs {
			t.Errorf("normalizeCommand(%q, %q) = (%q, %v), want (%q, %v)", tt.text, tt.username, got, forUs, tt.want, tt.forUs)
		}
	}
}

// A network error carries the request URL, and the Bot API puts the token
// in the URL; it must never reach the logs.
func TestRedactRemovesToken(t *testing.T) {
	const token = "123456:ABC-secret_token"
	b := New(token)
	err := &url.Error{Op: "Post", URL: apiBase + "/bot" + token + "/getUpdates", Err: errors.New("TLS handshake timeout")}

	got := b.redact(err).Error()
	if strings.Contains(got, token) || strings.Contains(got, "secret") {
		t.Fatalf("token leaked: %s", got)
	}
	if !strings.Contains(got, "getUpdates") || !strings.Contains(got, "TLS handshake timeout") {
		t.Errorf("redaction lost useful detail: %s", got)
	}
}
