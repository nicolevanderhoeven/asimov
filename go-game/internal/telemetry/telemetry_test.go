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
func TestRatingsUseTheAPIEndpoint(t *testing.T) {
	c := Config{GenerationEndpoint: "https://sigil.example.com/api/v1/generations:export", Instance: "1", Token: "t"}
	if got := GenerationConfig(c, nil).API.Endpoint; got != c.GenerationEndpoint {
		t.Fatalf("API endpoint %q, want the generation endpoint", got)
	}
	c.APIEndpoint = "https://api.example.com"
	if got := GenerationConfig(c, nil).API.Endpoint; got != c.APIEndpoint {
		t.Fatalf("API endpoint %q, want AGENTO11Y_API_ENDPOINT", got)
	}
}

func TestValidateNamesEveryMissingSettingInOrder(t *testing.T) {
	const want = "missing AGENTO11Y_ENDPOINT (or GRAFANA_CLOUD_SIGIL_ENDPOINT), GRAFANA_CLOUD_API_KEY, GRAFANA_CLOUD_INSTANCE_ID, OTLP_ENDPOINT, OTLP_HEADERS; configure Grafana telemetry or explicitly use --no-telemetry"
	for i := 0; i < 20; i++ {
		err := (Config{}).Validate()
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
}

func TestValidateRejectsTheREGIONPlaceholder(t *testing.T) {
	c := Config{
		Endpoint: "https://otlp-gateway-prod-REGION.grafana.net/otlp", Authorization: "Basic test",
		GenerationEndpoint: "https://sigil-prod-REGION.grafana.net", Instance: "1", Token: "t",
	}
	err := c.Validate()
	const want = "env.example's REGION placeholder is still in AGENTO11Y_ENDPOINT, OTLP_ENDPOINT; configure Grafana telemetry or explicitly use --no-telemetry"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	c.Endpoint, c.GenerationEndpoint = "https://otlp-gateway-prod-eu-west-0.grafana.net/otlp", "https://sigil-prod-eu-west-0.grafana.net"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredMeansAnyGrafanaCredential(t *testing.T) {
	// env.example's endpoints alone don't count as configured.
	fresh := Config{Endpoint: "https://otlp-gateway-prod-REGION.grafana.net/otlp", GenerationEndpoint: "https://sigil-prod-REGION.grafana.net"}
	if fresh.Configured() {
		t.Fatal("placeholder endpoints counted as configured")
	}
	for name, c := range map[string]Config{"instance": {Instance: "1"}, "token": {Token: "t"}, "headers": {Authorization: "Basic x"}} {
		if !c.Configured() {
			t.Fatalf("%s alone not counted as configured", name)
		}
	}
}
