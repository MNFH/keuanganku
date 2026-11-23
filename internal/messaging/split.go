package messaging

import (
	"strings"
	"unicode/utf16"
)

// SplitText cuts text into chunks of at most limit UTF-16 code units, for
// platforms that cap message length (Telegram and the Cloud API at 4096).
//
// Length is counted in UTF-16 units, not runes, because that is how Telegram
// counts: an emoji outside the Basic Multilingual Plane (🔴 💚 📝) is one
// rune but two units, and a list full of them would otherwise overrun the
// platform limit while staying under ours. Counting this way is never less
// safe for a platform that counts runes.
//
// It packs whole paragraphs (separated by a blank line) into each chunk, so
// a list whose entries are paragraphs is never cut mid-entry. Only a
// paragraph longer than limit on its own is split, first at line breaks and
// then, for a single over-long line, between runes (never inside a surrogate
// pair). The separators at the cuts are dropped; nothing else is.
func SplitText(text string, limit int) []string {
	if limit <= 0 || TextLen(text) <= limit {
		return []string{text}
	}
	return pack(strings.Split(text, "\n\n"), "\n\n", limit, func(p string) []string {
		return pack(strings.Split(p, "\n"), "\n", limit, func(line string) []string {
			return splitRunes(line, limit)
		})
	})
}

// TextLen is the length of s in UTF-16 code units, the unit SplitText's limit
// is measured in.
func TextLen(s string) int {
	n := 0
	for _, r := range s {
		n += runeLen(r)
	}
	return n
}

func runeLen(r rune) int {
	if utf16.IsSurrogate(r) || r < 0x10000 {
		return 1
	}
	return 2
}

// pack joins parts with sep into chunks of at most limit units. A part that
// alone exceeds limit is handed to tooLong and its pieces emitted as chunks.
func pack(parts []string, sep string, limit int, tooLong func(string) []string) []string {
	var chunks []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if curLen > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
			curLen = 0
		}
	}
	sepLen := TextLen(sep)
	for _, p := range parts {
		n := TextLen(p)
		switch {
		case n == 0:
			continue
		case n > limit:
			flush()
			chunks = append(chunks, tooLong(p)...)
		case curLen == 0:
			cur.WriteString(p)
			curLen = n
		case curLen+sepLen+n <= limit:
			cur.WriteString(sep)
			cur.WriteString(p)
			curLen += sepLen + n
		default:
			flush()
			cur.WriteString(p)
			curLen = n
		}
	}
	flush()
	return chunks
}

// splitRunes cuts s into pieces of at most limit units, between runes. A
// single rune wider than limit (only possible when limit is 1) still gets a
// piece of its own rather than being dropped.
func splitRunes(s string, limit int) []string {
	var out []string
	var cur strings.Builder
	curLen := 0
	for _, r := range s {
		n := runeLen(r)
		if curLen > 0 && curLen+n > limit {
			out = append(out, cur.String())
			cur.Reset()
			curLen = 0
		}
		cur.WriteRune(r)
		curLen += n
	}
	if curLen > 0 {
		out = append(out, cur.String())
	}
	return out
}
