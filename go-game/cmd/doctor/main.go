// Command doctor checks the game's configuration end to end: that every
// setting it needs is present and well formed, that the Anthropic key works,
// and that Grafana Cloud accepts one test span, metric, log line, and
// generation. It never prints a secret, only whether each one is set and
// what the server said about it.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/joho/godotenv"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const conversationTitle = "Setup check"

type report struct{ failed bool }

func (r *report) ok(format string, args ...any)   { fmt.Printf("  ✔ "+format+"\n", args...) }
func (r *report) warn(format string, args ...any) { fmt.Printf("  ! "+format+"\n", args...) }
func (r *report) fail(format string, args ...any) {
	r.failed = true
	fmt.Printf("  ✘ "+format+"\n", args...)
}

func main() {
	env := flag.String("env", "../.env", "Environment file; existing shell variables take precedence")
	flag.Parse()
	r := &report{}
	// Each check reports its own export error; OTel would print it again.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))

	fmt.Println("Configuration")
	switch err := godotenv.Load(*env); {
	case err == nil:
		r.ok("read %s", *env)
	case os.IsNotExist(err):
		r.warn("%s not found; using environment variables only (copy env.example to .env if you meant to use one)", *env)
	default:
		r.fail("could not read %s: %v", *env, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fmt.Println("\nAnthropic")
	checkAnthropic(ctx, r)

	fmt.Println("\nGrafana Cloud settings")
	cfg := telemetry.FromEnv()
	settingsOK := checkSettings(r, cfg)

	if settingsOK {
		if cfg.LocalCollector() {
			fmt.Println("\nDelivery (sends one test item of each kind; OTLP goes to the local Collector)")
		} else {
			fmt.Println("\nGrafana Cloud delivery (sends one test item of each kind)")
		}
		res := resource.NewSchemaless(attribute.String("service.name", telemetry.Service), attribute.String("service.version", cfg.Version))
		report := func(signal, where string, err error) {
			if err != nil {
				r.fail("%s rejected: %s", signal, explain(err))
				return
			}
			r.ok("%s accepted (%s)", signal, where)
		}
		report("trace", `Explore → Traces: service "`+telemetry.Service+`", span "setup.check"`, sendSpan(ctx, cfg, res))
		report("metric", "Explore → Metrics: asimov_setup_check_total", sendMetric(ctx, cfg, res))
		report("log", `Explore → Logs: service_name="`+telemetry.Service+`"`, sendLog(ctx, cfg, res))
		report("generation", `Agent Observability → Conversations: "`+conversationTitle+`"`, sendGeneration(ctx, cfg))
	}

	fmt.Println()
	if r.failed {
		fmt.Println("Some checks failed. See docs/grafana-cloud-setup.md for where each value comes from.")
		os.Exit(1)
	}
	fmt.Println("All checks passed. Data can take a minute or two to appear in Grafana.")
}

// checkAnthropic lists models, which costs nothing, to prove the key works.
func checkAnthropic(ctx context.Context, r *report) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		r.fail("ANTHROPIC_API_KEY is not set (only --offline play works without it)")
		return
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models?limit=1", nil)
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.fail("could not reach api.anthropic.com: %v", err)
		return
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		r.ok("ANTHROPIC_API_KEY works")
	case http.StatusUnauthorized, http.StatusForbidden:
		r.fail("ANTHROPIC_API_KEY was rejected (HTTP %d); create a new key at console.anthropic.com", resp.StatusCode)
	default:
		r.warn("api.anthropic.com answered HTTP %d; the key may still work", resp.StatusCode)
	}
}

// checkSettings reports each Grafana Cloud setting as set or missing,
// showing only endpoint hosts and never a token.
func checkSettings(r *report, cfg telemetry.Config) bool {
	ok := true
	need := func(name, value string, show bool) {
		switch {
		case strings.TrimSpace(value) == "":
			r.fail("%s is not set", name)
			ok = false
		case show:
			r.ok("%s = %s", name, value)
		default:
			r.ok("%s is set", name)
		}
	}
	need("OTLP_ENDPOINT", hostOf(cfg.Endpoint), true)
	if cfg.LocalCollector() {
		r.ok("OTLP goes to a local Collector, which holds the Grafana Cloud credentials")
	} else {
		need("OTLP_HEADERS", cfg.Authorization, false)
		if cfg.Authorization != "" {
			checkOTLPHeaders(r, strings.TrimPrefix(cfg.Authorization, "Basic "))
		}
	}
	need("AGENTO11Y_ENDPOINT", hostOf(cfg.GenerationEndpoint), true)
	need("GRAFANA_CLOUD_INSTANCE_ID", cfg.Instance, true)
	need("GRAFANA_CLOUD_API_KEY", cfg.Token, false)
	for name, endpoint := range map[string]string{"OTLP_ENDPOINT": cfg.Endpoint, "AGENTO11Y_ENDPOINT": cfg.GenerationEndpoint} {
		if strings.Contains(endpoint, "REGION") {
			r.fail("%s still has env.example's REGION placeholder; copy the real URL from Grafana Cloud", name)
			ok = false
		}
	}
	if strings.TrimSpace(cfg.Token) != "" && !strings.HasPrefix(cfg.Token, "glc_") {
		r.warn("GRAFANA_CLOUD_API_KEY doesn't start with glc_; Cloud Access Policy tokens usually do")
	}
	if ok {
		if err := cfg.Validate(); err != nil {
			r.fail("%v", err)
			return false
		}
	}
	return ok
}

// checkOTLPHeaders decodes OTLP_HEADERS enough to catch the usual mistakes
// (not base64, or no colon), showing only the instance ID half.
func checkOTLPHeaders(r *report, encoded string) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		r.fail("OTLP_HEADERS isn't base64; build it with: printf '%%s' 'INSTANCE_ID:TOKEN' | base64")
		return
	}
	id, token, found := strings.Cut(string(raw), ":")
	switch {
	case !found || id == "" || token == "":
		r.fail(`OTLP_HEADERS should decode to "INSTANCE_ID:TOKEN"; build it with: printf '%%s' 'INSTANCE_ID:TOKEN' | base64`)
	case strings.ContainsAny(token, " \n\r"):
		r.fail("OTLP_HEADERS has whitespace in its token; use printf '%%s', not echo, when encoding it")
	default:
		r.ok("OTLP_HEADERS decodes to OTLP instance %s plus a token", id)
	}
}

func hostOf(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host + u.Path
	}
	return endpoint
}

// explain turns common export errors into a next step.
func explain(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "401"), strings.Contains(msg, "Unauthorized"):
		return msg + " (wrong instance ID or token)"
	case strings.Contains(msg, "403"), strings.Contains(msg, "Forbidden"):
		return msg + " (the token is missing a scope: it needs metrics:write, logs:write, traces:write, and sigil:write)"
	case strings.Contains(msg, "no such host"):
		return msg + " (check the endpoint's hostname)"
	case strings.Contains(msg, "404"):
		return msg + " (check the endpoint URL)"
	}
	return msg
}

// spanCapture, logCapture: OTel's processors report export errors to a
// global handler; these keep the error for the caller instead.
type spanCapture struct {
	sdktrace.SpanExporter
	err error
}

func (c *spanCapture) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	c.err = c.SpanExporter.ExportSpans(ctx, spans)
	return c.err
}

type logCapture struct {
	sdklog.Exporter
	err error
}

func (c *logCapture) Export(ctx context.Context, records []sdklog.Record) error {
	c.err = c.Exporter.Export(ctx, records)
	return c.err
}

func otlpURL(cfg telemetry.Config, signal string) string {
	return strings.TrimRight(cfg.Endpoint, "/") + "/v1/" + signal
}

func sendSpan(ctx context.Context, cfg telemetry.Config, res *resource.Resource) error {
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(otlpURL(cfg, "traces")), otlptracehttp.WithHeaders(cfg.OTLPHeaders()), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
	if err != nil {
		return err
	}
	capture := &spanCapture{SpanExporter: exp}
	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSyncer(capture))
	_, span := tp.Tracer("asimov/doctor").Start(ctx, "setup.check")
	span.End()
	return errors.Join(capture.err, tp.Shutdown(ctx))
}

func sendMetric(ctx context.Context, cfg telemetry.Config, res *resource.Resource) error {
	exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(otlpURL(cfg, "metrics")), otlpmetrichttp.WithHeaders(cfg.OTLPHeaders()), otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: false}))
	if err != nil {
		return err
	}
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(reader))
	counter, err := mp.Meter("asimov/doctor").Int64Counter("asimov.setup_check")
	if err != nil {
		return err
	}
	counter.Add(ctx, 1)
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		return err
	}
	return errors.Join(exp.Export(ctx, &rm), exp.Shutdown(ctx), mp.Shutdown(ctx))
}

func sendLog(ctx context.Context, cfg telemetry.Config, res *resource.Resource) error {
	exp, err := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(otlpURL(cfg, "logs")), otlploghttp.WithHeaders(cfg.OTLPHeaders()), otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: false}))
	if err != nil {
		return err
	}
	capture := &logCapture{Exporter: exp}
	lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewSimpleProcessor(capture)))
	slog.New(otelslog.NewHandler(telemetry.Service, otelslog.WithLoggerProvider(lp))).InfoContext(ctx, "setup check")
	return errors.Join(capture.err, lp.Shutdown(ctx))
}

// sendGeneration records one fake generation and flushes it, which returns
// the export error the game would otherwise only log.
func sendGeneration(ctx context.Context, cfg telemetry.Config) error {
	client := agento11y.NewClient(telemetry.GenerationConfig(cfg, log.New(io.Discard, "", 0)))
	_, rec := client.StartGeneration(ctx, agento11y.GenerationStart{
		ConversationID:    fmt.Sprintf("setup-check-%d", time.Now().Unix()),
		ConversationTitle: conversationTitle,
		Model:             agento11y.ModelRef{Provider: "none", Name: "setup-check"},
		// Not narration or action_resolution, so no online evaluator scores it.
		Tags: map[string]string{"component": "setup_check"},
	})
	rec.SetResult(agento11y.Generation{
		Input:  []agento11y.Message{agento11y.UserTextMessage("Is telemetry reaching Grafana Cloud?")},
		Output: []agento11y.Message{agento11y.AssistantTextMessage("Yes: this generation arrived.")},
	}, nil)
	rec.End()
	return errors.Join(rec.Err(), client.Flush(ctx), client.Shutdown(ctx))
}
