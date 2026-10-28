package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/grafana/agento11y/go/agento11y"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

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

func TestOTLPAloneIsValid(t *testing.T) {
	for _, c := range []Config{
		{Endpoint: "http://localhost:4318"},
		{Endpoint: "https://otlp.example.com/otlp", Authorization: "Basic dGVzdDp0ZXN0"},
	} {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", c.Endpoint, err)
		}
		if c.GenerationConfigured() {
			t.Errorf("%s: Agent Observability reported configured", c.Endpoint)
		}
	}
}

func TestGenerationAloneIsValid(t *testing.T) {
	c := Config{GenerationEndpoint: "https://sigil.example.com", Instance: "1", Token: "t"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.OTLPConfigured() {
		t.Fatal("OTLP reported configured")
	}
}

func TestPartialSettingsAreReportedByName(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Config
		want []string
	}{
		{"generation endpoint only", Config{Endpoint: "http://localhost:4318", GenerationEndpoint: "https://sigil.example.com"}, []string{"GRAFANA_CLOUD_INSTANCE_ID", "GRAFANA_CLOUD_API_KEY"}},
		{"token only", Config{Endpoint: "http://localhost:4318", Token: "t"}, []string{"AGENTO11Y_ENDPOINT", "GRAFANA_CLOUD_INSTANCE_ID"}},
		{"api endpoint only", Config{Endpoint: "http://localhost:4318", APIEndpoint: "https://api.example.com"}, []string{"AGENTO11Y_ENDPOINT", "GRAFANA_CLOUD_API_KEY"}},
		{"cloud OTLP without headers", Config{Endpoint: "https://otlp.example.com/otlp"}, []string{"OTLP_HEADERS"}},
		{"OTLP headers without endpoint", Config{Authorization: "Basic dGVzdDp0ZXN0"}, []string{"OTLP_ENDPOINT"}},
	} {
		err := c.cfg.Validate()
		if err == nil {
			t.Errorf("%s: partial settings accepted", c.name)
			continue
		}
		for _, want := range c.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q doesn't name %s", c.name, err, want)
			}
		}
	}
}

func TestNothingConfiguredPointsToTheLocalStack(t *testing.T) {
	err := (Config{}).Validate()
	if err == nil || !strings.Contains(err.Error(), "make local-stack") || !strings.Contains(err.Error(), "--no-telemetry") {
		t.Fatalf("error %v should point to make local-stack and --no-telemetry", err)
	}
}

func TestOnlyLocalOTLPMayUsePlainHTTP(t *testing.T) {
	if (Config{Endpoint: "http://example.com", Authorization: "Basic test"}).Validate() == nil {
		t.Fatal("plain-HTTP OTLP to a remote host accepted")
	}
	if (Config{Endpoint: "http://localhost:4318", GenerationEndpoint: "http://localhost:9999", Instance: "1", Token: "t"}).Validate() == nil {
		t.Fatal("plain-HTTP Agent Observability endpoint accepted")
	}
}

func TestGenerationsFileNeedsAgentObservabilityOff(t *testing.T) {
	c := Config{Endpoint: "http://localhost:4318", GenerationsFile: "generations.jsonl"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.GenerationEndpoint, c.Instance, c.Token = "https://sigil.example.com", "1", "t"
	if c.Validate() == nil {
		t.Fatal("a local generations file next to Agent Observability accepted")
	}
}

func TestGenerationExportIsOffWithoutSettings(t *testing.T) {
	cfg := GenerationConfig(Config{Endpoint: "http://localhost:4318"}, nil)
	if cfg.GenerationExport.Protocol != agento11y.GenerationExportProtocolNone {
		t.Fatalf("protocol %q, want none", cfg.GenerationExport.Protocol)
	}
	on := GenerationConfig(Config{GenerationEndpoint: "https://sigil.example.com", Instance: "1", Token: "t"}, nil)
	if on.GenerationExport.Protocol != agento11y.GenerationExportProtocolHTTP {
		t.Fatalf("protocol %q, want http", on.GenerationExport.Protocol)
	}
}

// With Agent Observability off, a model call is still a span with the
// standard gen-AI attributes and a sample in the gen_ai.client.* metrics, so
// it can be inspected in Tempo and Prometheus.
func TestGenerationsAreSpansWithoutAgentObservability(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	cfg := GenerationConfig(Config{Endpoint: "http://localhost:4318", Version: "test-v1"}, nil)
	cfg.Tracer = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)).Tracer("test")
	cfg.Meter = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
	client := agento11y.NewClient(cfg)
	_, rec := client.StartGeneration(context.Background(), agento11y.GenerationStart{
		ConversationID: "c1", Model: agento11y.ModelRef{Provider: "anthropic", Name: "claude-test"},
	})
	rec.SetResult(agento11y.Generation{
		Input:  []agento11y.Message{agento11y.UserTextMessage("hello")},
		Output: []agento11y.Message{agento11y.AssistantTextMessage("hi")},
		Usage:  agento11y.TokenUsage{InputTokens: 11, OutputTokens: 7},
	}, nil)
	rec.End()
	if err := rec.Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	ended := spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("%d spans, want 1", len(ended))
	}
	got := map[string]string{}
	for _, kv := range ended[0].Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	for key, want := range map[string]string{
		"gen_ai.operation.name":      "generateText",
		"gen_ai.provider.name":       "anthropic",
		"gen_ai.request.model":       "claude-test",
		"gen_ai.usage.input_tokens":  "11",
		"gen_ai.usage.output_tokens": "7",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	for _, want := range []string{"gen_ai.client.operation.duration", "gen_ai.client.token.usage"} {
		if !names[want] {
			t.Errorf("no %s metric; got %v", want, names)
		}
	}
}

func TestInitWithOTLPAloneKeepsGenerationsLocally(t *testing.T) {
	var requests atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	// httptest listens on 127.0.0.1, so it counts as a local receiver.
	file := filepath.Join(t.TempDir(), "logs", "generations.jsonl")
	r, err := Init(context.Background(), Config{Endpoint: collector.URL, Version: "test-v1", GenerationsFile: file}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r.Client == nil || r.GenerationExport {
		t.Fatalf("client %v, generation export %v; want a client with export off", r.Client, r.GenerationExport)
	}
	_, rec := r.Client.StartGeneration(context.Background(), agento11y.GenerationStart{
		ConversationID: "c1", Model: agento11y.ModelRef{Provider: "anthropic", Name: "claude-test"},
		SystemPrompt: "You are the GM.", Tags: map[string]string{"component": "narration"},
	})
	rec.SetResult(agento11y.Generation{
		Input:  []agento11y.Message{agento11y.UserTextMessage("I read the log")},
		Output: []agento11y.Message{agento11y.AssistantTextMessage("The log says...")},
	}, nil)
	rec.End()
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("%d lines in the generations file, want 1:\n%s", len(lines), data)
	}
	var gen map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &gen); err != nil {
		t.Fatal(err)
	}
	if gen["system_prompt"] != "You are the GM." || gen["conversation_id"] != "c1" || gen["agent_version"] != "test-v1" {
		t.Errorf("generation %v lacks its system prompt, conversation, or agent version", gen)
	}
	if tags, _ := gen["tags"].(map[string]any); tags["component"] != "narration" {
		t.Errorf("tags %v lack component=narration", gen["tags"])
	}
	if requests.Load() == 0 {
		t.Error("no OTLP request reached the receiver")
	}
}

func TestValidateNamesEveryMissingSettingInOrder(t *testing.T) {
	// Agent Observability and Cloud OTLP both half set, so everything they
	// need is named at once, in sorted order.
	c := Config{Endpoint: "https://otlp.example.com/otlp", APIEndpoint: "https://api.example.com"}
	const want = "missing AGENTO11Y_ENDPOINT (or GRAFANA_CLOUD_SIGIL_ENDPOINT), GRAFANA_CLOUD_API_KEY, GRAFANA_CLOUD_INSTANCE_ID, OTLP_HEADERS; Agent Observability needs all of AGENTO11Y_ENDPOINT, GRAFANA_CLOUD_INSTANCE_ID, and GRAFANA_CLOUD_API_KEY, or none of them; configure Grafana telemetry or explicitly use --no-telemetry"
	for i := 0; i < 20; i++ {
		err := c.Validate()
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
