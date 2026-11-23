package messaging

import (
	"math/rand"
	"strings"
	"testing"
)

func TestSplitText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		limit int
		want  []string
	}{
		{"fits", "a\n\nb", 10, []string{"a\n\nb"}},
		{"exactly at limit", "abcd\n\nef", 8, []string{"abcd\n\nef"}},
		{"one over packs paragraphs", "abcd\n\nefg", 8, []string{"abcd", "efg"}},
		{"keeps paragraphs whole", "aa\nbb\n\ncc\ndd\n\nee", 6, []string{"aa\nbb", "cc\ndd", "ee"}},
		{"huge paragraph falls back to lines", "x\n\naaaa\nbbbb\ncccc\n\ny", 9, []string{"x", "aaaa\nbbbb", "cccc", "y"}},
		{"huge line falls back to runes", "ab\n\n0123456789", 4, []string{"ab", "0123", "4567", "89"}},
		{"counts UTF-16 units, not bytes", "éé\n\né", 2, []string{"éé", "é"}},
		{"emoji outside the BMP count as two", "📜📜📜\n\n📜", 6, []string{"📜📜📜", "📜"}},
		{"emoji push a paragraph over", "📜📜\n\nab", 5, []string{"📜📜", "ab"}},
		{"rune fallback never splits an emoji", "x\n\n📜a📜", 3, []string{"x", "📜a", "📜"}},
		{"an emoji wider than the limit stays whole", "📜a", 1, []string{"📜", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitText(tt.text, tt.limit)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("SplitText = %q, want %q", got, tt.want)
			}
		})
	}
}

// Property 5: splitting loses nothing and respects the limit, and a paragraph
// is split only when it alone exceeds the limit.
func TestPropertySplitText(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	alphabet := []rune("ab cd📜é*_")
	for i := 0; i < 3000; i++ {
		var paras []string
		for n := r.Intn(12); n >= 0; n-- {
			var lines []string
			for m := r.Intn(4); m >= 0; m-- {
				line := make([]rune, r.Intn(40))
				for k := range line {
					line[k] = alphabet[r.Intn(len(alphabet))]
				}
				lines = append(lines, string(line))
			}
			paras = append(paras, strings.Join(lines, "\n"))
		}
		text := strings.Join(paras, "\n\n")
		// At least 2, so any single rune fits; a lone emoji wider than the
		// limit is the one case a chunk may exceed it (tested above).
		limit := 2 + r.Intn(80)
		chunks := SplitText(text, limit)

		if TextLen(text) > limit {
			for _, c := range chunks {
				if TextLen(c) > limit {
					t.Fatalf("case %d: chunk of %d UTF-16 units, limit %d", i, TextLen(c), limit)
				}
			}
		}
		strip := func(s string) string { return strings.ReplaceAll(s, "\n", "") }
		if strip(strings.Join(chunks, "")) != strip(text) {
			t.Fatalf("case %d: content lost or reordered", i)
		}
		// Paragraphs as the text defines them: an empty generated line can
		// merge separators, so the generator's own list is not the truth.
		for _, p := range strings.Split(text, "\n\n") {
			if p == "" || TextLen(p) > limit {
				continue
			}
			whole := false
			for _, c := range chunks {
				if strings.Contains(c, p) {
					whole = true
					break
				}
			}
			if !whole {
				t.Fatalf("case %d: paragraph %q (fits in %d) was split", i, p, limit)
			}
		}
	}
}
