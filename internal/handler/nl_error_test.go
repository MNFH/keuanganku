package handler

import "testing"

func TestLooksLikeEntry(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want bool
	}{
		{"amount with k", Message{Text: "Somay 15k jago personal"}, true},
		{"plain amount", Message{Text: "Sate taichan 133500"}, true},
		{"photo without caption", Message{Image: []byte{1}}, true},
		{"photo with caption", Message{Text: "Jago belanja", Image: []byte{1}}, true},
		{"chat", Message{Text: "mantap"}, false},
		{"chat sentence", Message{Text: "yg mingguan wkwk"}, false},
		{"empty", Message{}, false},
	}
	for _, c := range cases {
		if got := looksLikeEntry(c.msg); got != c.want {
			t.Errorf("%s: looksLikeEntry(%q) = %v, want %v", c.name, c.msg.Text, got, c.want)
		}
	}
}
