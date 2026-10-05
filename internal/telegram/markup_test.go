package telegram

import "testing"

func TestToHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "halo", "halo"},
		{"bold", "*Pengeluaran* dicatat", "<b>Pengeluaran</b> dicatat"},
		{"italic", "_via: /saldo_", "<i>via: /saldo</i>"},
		{"both", "*Total* _Rp 5.000_", "<b>Total</b> <i>Rp 5.000</i>"},
		{"bold spanning words", "*Rekap Bulanan*", "<b>Rekap Bulanan</b>"},
		{"two bold runs", "*a* dan *b*", "<b>a</b> dan <b>b</b>"},

		// Escaping has to happen before markup, or Telegram rejects the whole
		// message as malformed entities and the reply is lost outright.
		{"ampersand escaped", "Untung & Rugi", "Untung &amp; Rugi"},
		// A single underscore inside a word has no pair, so it stays literal —
		// snake_case placeholders must survive intact.
		{"angle brackets escaped", "/daftar <spreadsheet_id>", "/daftar &lt;spreadsheet_id&gt;"},
		{"two snake_case words would pair", "a_b c_d", "a<i>b c</i>d"},
		{"escaping precedes markup", "*A & B*", "<b>A &amp; B</b>"},

		// Lone or unmatched markers must stay literal.
		{"lone asterisk", "2 * 3", "2 * 3"},
		{"unmatched opener", "*unclosed", "*unclosed"},
		{"asterisk with spaces inside", "a * b * c", "a * b * c"},
		{"marker does not cross lines", "*not\nbold*", "*not\nbold*"},
		{"empty pair", "**", "**"},

		// Real replies from the bot.
		{
			name: "expense confirmation",
			in:   "🔴 *Pengeluaran* dicatat!\n💳 Dompet: GoPay",
			want: "🔴 <b>Pengeluaran</b> dicatat!\n💳 Dompet: GoPay",
		},
		{
			name: "undo hint",
			in:   "_Salah? Kirim */batal*_",
			want: "<i>Salah? Kirim <b>/batal</b></i>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToHTML(tt.in); got != tt.want {
				t.Errorf("ToHTML(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}
