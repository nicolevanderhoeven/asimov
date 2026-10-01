package main

import (
	"encoding/base64"
	"testing"
)

func TestCheckOTLPHeaders(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for _, c := range []struct {
		name, encoded string
		fails         bool
	}{
		{"valid", b64("123456:glc_token"), false},
		{"not base64", "123456:glc_token", true},
		{"no colon", b64("glc_token"), true},
		{"empty token", b64("123456:"), true},
		{"echo newline", b64("123456:glc_token\n"), true},
	} {
		r := &report{}
		checkOTLPHeaders(r, c.encoded)
		if r.failed != c.fails {
			t.Errorf("%s: failed = %v, want %v", c.name, r.failed, c.fails)
		}
	}
}
