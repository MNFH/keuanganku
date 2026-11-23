package ai

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"google.golang.org/genai"
)

func TestSplitModels(t *testing.T) {
	cases := map[string][]string{
		"gemini-3.1-flash-lite": {"gemini-3.1-flash-lite"},
		"a, b ,c":               {"a", "b", "c"},
		"a,,b,a":                {"a", "b"},
		"":                      nil,
		" , ":                   nil,
		"gemini-3.1-flash-lite,gemini-3.5-flash,x": {"gemini-3.1-flash-lite", "gemini-3.5-flash", "x"},
	}
	for in, want := range cases {
		if got := splitModels(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitModels(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTryNextModel(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{503, true}, {429, true}, {500, true}, {502, true}, {504, true},
		{404, true}, // retired model ID
		{400, false}, {401, false}, {403, false},
	}
	for _, c := range cases {
		err := fmt.Errorf("wrapped: %w", genai.APIError{Code: c.code})
		if got := tryNextModel(err); got != c.want {
			t.Errorf("tryNextModel(%d) = %v, want %v", c.code, got, c.want)
		}
	}
	if tryNextModel(errors.New("network down")) {
		t.Errorf("tryNextModel(non-API error) = true, want false")
	}
}
