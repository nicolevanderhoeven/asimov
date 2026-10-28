package main

import (
	"encoding/base64"
	"testing"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
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

func TestCheckSettings(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  telemetry.Config
		ok   bool
	}{
		{"OTLP to the local stack alone", telemetry.Config{Endpoint: "http://localhost:4318"}, true},
		{"Agent Observability alone", telemetry.Config{GenerationEndpoint: "https://sigil.example.com", Instance: "1", Token: "glc_t"}, true},
		{"both", telemetry.Config{Endpoint: "http://localhost:4318", GenerationEndpoint: "https://sigil.example.com", Instance: "1", Token: "glc_t"}, true},
		{"nothing", telemetry.Config{}, false},
		{"half of Agent Observability", telemetry.Config{Endpoint: "http://localhost:4318", GenerationEndpoint: "https://sigil.example.com"}, false},
		{"Cloud OTLP without headers", telemetry.Config{Endpoint: "https://otlp.example.com/otlp"}, false},
		{"the REGION placeholder", telemetry.Config{Endpoint: "https://otlp-gateway-prod-REGION.grafana.net/otlp", Authorization: "Basic dGVzdDp0ZXN0"}, false},
	} {
		r := &report{}
		if got := checkSettings(r, c.cfg); got != c.ok || r.failed == c.ok {
			t.Errorf("%s: checkSettings = %v (failed %v), want %v", c.name, got, r.failed, c.ok)
		}
	}
}
