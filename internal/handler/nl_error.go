package handler

import "strings"

// parseFailedMsg is sent when the AI could not read a message that looked
// like a transaction, so the sender knows nothing was recorded.
const parseFailedMsg = "❌ Gagal membaca pesan, belum dicatat. Layanan AI sedang sibuk; kirim ulang sebentar lagi atau pakai perintah manual (/help)."

// looksLikeEntry reports whether a free-form message is probably a
// transaction, so a failure to parse it deserves a reply. A receipt photo
// always counts; text counts when it contains a digit, since every amount
// does ("15k", "133500") and ordinary group chat mostly does not.
func looksLikeEntry(msg Message) bool {
	if len(msg.Image) > 0 {
		return true
	}
	return strings.ContainsAny(msg.Text, "0123456789")
}
