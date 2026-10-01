package telemetry

import "testing"

func TestLegacyEnvCompatibility(t *testing.T) {
	t.Setenv("AGENTO11Y_ENDPOINT", "")
	t.Setenv("GRAFANA_CLOUD_SIGIL_ENDPOINT", "https://example.com/api/v1/generations:export")
	t.Setenv("GRAFANA_CLOUD_INSTANCE_ID", "123")
	t.Setenv("GRAFANA_CLOUD_API_KEY", "example-token")
	t.Setenv("OTLP_ENDPOINT", "https://example.com/otlp")
	t.Setenv("OTLP_HEADERS", "dGVzdDp0ZXN0")
	c := FromEnv()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Authorization != "Basic dGVzdDp0ZXN0" || c.Instance != "123" {
		t.Fatal("legacy config mapping failed")
	}
}
func TestIncompleteTelemetryFailsClearly(t *testing.T) {
	if (Config{}).Validate() == nil {
		t.Fatal("missing telemetry silently accepted")
	}
}
func TestRejectInsecureEndpoint(t *testing.T) {
	c := Config{Endpoint: "http://example.com", Authorization: "Basic test", GenerationEndpoint: "https://example.com/export", Instance: "test", Token: "test"}
	if c.Validate() == nil {
		t.Fatal("insecure export accepted")
	}
	c.GenerationEndpoint = "http://localhost:4318/export"
	c.Endpoint = "https://example.com/otlp"
	if c.Validate() == nil {
		t.Fatal("insecure generation export accepted")
	}
}
func TestLocalCollectorNeedsNoOTLPAuth(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:4318", "http://127.0.0.1:4318", "http://[::1]:4318"} {
		c := Config{Endpoint: endpoint, GenerationEndpoint: "https://example.com/export", Instance: "test", Token: "test"}
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
		if c.OTLPHeaders() != nil {
			t.Fatalf("%s: unexpected OTLP auth header", endpoint)
		}
	}
	if (Config{Endpoint: "https://example.com/otlp", GenerationEndpoint: "https://example.com/export", Instance: "test", Token: "test"}).Validate() == nil {
		t.Fatal("missing OTLP_HEADERS accepted for Grafana Cloud")
	}
}
