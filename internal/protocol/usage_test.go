package protocol

import "testing"

func TestExtractUsage(t *testing.T) {
	cases := []struct {
		name, body string
		in, out    int
		ok         bool
	}{
		{"anthropic", `{"usage":{"input_tokens":11,"output_tokens":7}}`, 11, 7, true},
		{"openai", `{"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`, 5, 3, true},
		{"responses", `{"usage":{"input_tokens":9,"output_tokens":4}}`, 9, 4, true},
		{"missing", `{"id":"x"}`, 0, 0, false},
		{"zero", `{"usage":{"input_tokens":0,"output_tokens":0}}`, 0, 0, false},
		{"invalid", `{broken`, 0, 0, false},
	}
	for _, tc := range cases {
		in, out, ok := ExtractUsage([]byte(tc.body))
		if in != tc.in || out != tc.out || ok != tc.ok {
			t.Fatalf("%s: got %d,%d,%v want %d,%d,%v", tc.name, in, out, ok, tc.in, tc.out, tc.ok)
		}
	}
}
