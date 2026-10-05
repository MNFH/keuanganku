package telegram

import "strings"

// ToHTML converts the bot's WhatsApp-flavoured markup to the Telegram HTML
// Telegram's parse_mode understands: *bold* becomes <b>bold</b> and _italic_
// becomes <i>italic</i>.
//
// HTML special characters are escaped first, so a reply containing "&" or "<"
// can't produce malformed markup — Telegram rejects the whole message when
// the entities don't parse, which would turn a cosmetic problem into a lost
// reply.
//
// Markers are only honoured as a matched pair on the same line. A lone "*"
// (a multiplication sign, a bullet) or an underscore inside a word
// (snake_case, a file name) is left as literal text, which is the common case
// in amounts, descriptions and error messages.
func ToHTML(s string) string {
	s = escapeHTML(s)
	s = convertPairs(s, '*', "b")
	s = convertPairs(s, '_', "i")
	return s
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// convertPairs wraps text delimited by matched marker runes in an HTML tag.
func convertPairs(s string, marker byte, tag string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != marker {
			b.WriteByte(c)
			continue
		}

		close := matchingMarker(s, i, marker)
		if close < 0 {
			b.WriteByte(c)
			continue
		}

		b.WriteString("<" + tag + ">")
		b.WriteString(s[i+1 : close])
		b.WriteString("</" + tag + ">")
		i = close
	}
	return b.String()
}

// matchingMarker finds the closing marker for the one at open, or -1 if this
// occurrence should be treated as literal text. The span must be non-empty,
// must not cross a line break, and must not begin or end on whitespace —
// which is what distinguishes "*bold*" from "2 * 3 * 4".
func matchingMarker(s string, open int, marker byte) int {
	if open+1 >= len(s) || isSpace(s[open+1]) {
		return -1
	}
	// Start at open+2 so the span is never empty: "**" stays literal rather
	// than becoming a pointless empty tag.
	for i := open + 2; i < len(s); i++ {
		switch {
		case s[i] == '\n':
			return -1
		case s[i] == marker && !isSpace(s[i-1]):
			return i
		}
	}
	return -1
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
