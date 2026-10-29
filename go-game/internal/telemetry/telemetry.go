package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const Service = "asimov-enterprise-go"

// Config holds the telemetry settings. Endpoint and Authorization are OTLP
// (traces, metrics, logs); GenerationEndpoint, Instance, and Token are Agent
// Observability, and each group works without the other. APIEndpoint is the
// Agent Observability API that conversation ratings go to; when empty, the
// SDK uses the host of GenerationEndpoint. GenerationsFile, only without
// Agent Observability, is a local JSON-lines file that every generation is
// also written to, for agento11y/judge-local.py.
type Config struct {
	Endpoint, Authorization, GenerationEndpoint, APIEndpoint, Instance, Token, Version string
	GenerationsFile                                                                    string
}

func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func FromEnv() Config {
	auth := strings.TrimSpace(os.Getenv("OTLP_HEADERS"))
	if auth != "" && !strings.HasPrefix(auth, "Basic ") {
		auth = "Basic " + auth
	}
	return Config{
		Endpoint: os.Getenv("OTLP_ENDPOINT"), Authorization: auth,
		GenerationEndpoint: first(os.Getenv("AGENTO11Y_ENDPOINT"), os.Getenv("GRAFANA_CLOUD_SIGIL_ENDPOINT")),
		APIEndpoint:        os.Getenv("AGENTO11Y_API_ENDPOINT"),
		Instance:           first(os.Getenv("GRAFANA_CLOUD_INSTANCE_ID"), os.Getenv("GRAFANA_CLOUD_INSTANCE")),
		Token:              os.Getenv("GRAFANA_CLOUD_API_KEY"), Version: first(os.Getenv("ASIMOV_AGENT_VERSION"), "go-experiment-v1"),
		GenerationsFile: strings.TrimSpace(os.Getenv("ASIMOV_GENERATIONS_FILE")),
	}
}

// Configured reports whether c holds any Grafana Cloud credential, which
// means the user meant to send telemetry. The endpoints don't count: a fresh
// copy of an old env.example already fills them with placeholders.
func (c Config) Configured() bool {
	return first(c.Instance, c.Token, c.Authorization) != ""
}

// OTLPConfigured reports whether any OTLP setting is present, so that
// traces, metrics, and logs are exported.
func (c Config) OTLPConfigured() bool {
	return strings.TrimSpace(c.Endpoint) != "" || c.Authorization != ""
}

// GenerationConfigured reports whether any Agent Observability setting is
// present. Agent Observability is a Grafana Cloud service, so a run with none
// of them records model calls only as local spans and metrics.
func (c Config) GenerationConfigured() bool {
	return first(c.GenerationEndpoint, c.APIEndpoint, c.Instance, c.Token) != ""
}

// Validate reports every missing or placeholder setting at once, in sorted
// order, so one run shows the whole fix. OTLP (traces, metrics, logs) and
// Agent Observability generation export are independent: either one alone is
// valid, and each must be complete once any of its settings is present, so a
// half-filled Grafana Cloud configuration is reported instead of silently
// ignored. Having neither is an error too, so that a run never loses its
// telemetry without asking (--no-telemetry does that explicitly).
func (c Config) Validate() error {
	otlp, generation := c.OTLPConfigured(), c.GenerationConfigured()
	if !otlp && !generation {
		return errors.New("no telemetry is configured; set OTLP_ENDPOINT (run make local-stack for a local Grafana, or see docs/grafana-cloud-setup.md for Grafana Cloud) or explicitly use --no-telemetry")
	}
	var missing []string
	if otlp {
		if strings.TrimSpace(c.Endpoint) == "" {
			missing = append(missing, "OTLP_ENDPOINT")
		}
		// A local Collector or stack (see collector/ and local-stack/) takes
		// unauthenticated OTLP; Grafana Cloud needs Basic auth.
		if !c.LocalCollector() && c.Authorization == "" {
			missing = append(missing, "OTLP_HEADERS")
		}
	}
	partial := false
	if generation {
		for name, v := range map[string]string{"AGENTO11Y_ENDPOINT (or GRAFANA_CLOUD_SIGIL_ENDPOINT)": c.GenerationEndpoint, "GRAFANA_CLOUD_INSTANCE_ID": c.Instance, "GRAFANA_CLOUD_API_KEY": c.Token} {
			if strings.TrimSpace(v) == "" {
				missing, partial = append(missing, name), true
			}
		}
	}
	sort.Strings(missing)
	// env.example's REGION placeholder means the URL was never filled in.
	var placeholders []string
	for name, v := range map[string]string{"AGENTO11Y_API_ENDPOINT": c.APIEndpoint, "AGENTO11Y_ENDPOINT": c.GenerationEndpoint, "OTLP_ENDPOINT": c.Endpoint} {
		if strings.Contains(v, "REGION") {
			placeholders = append(placeholders, name)
		}
	}
	sort.Strings(placeholders)
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "missing "+strings.Join(missing, ", "))
	}
	if partial {
		problems = append(problems, "Agent Observability needs all of AGENTO11Y_ENDPOINT, GRAFANA_CLOUD_INSTANCE_ID, and GRAFANA_CLOUD_API_KEY, or none of them")
	}
	if len(placeholders) > 0 {
		problems = append(problems, "env.example's REGION placeholder is still in "+strings.Join(placeholders, ", "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s; configure Grafana telemetry or explicitly use --no-telemetry", strings.Join(problems, "; "))
	}
	if c.GenerationsFile != "" && generation {
		return errors.New("ASIMOV_GENERATIONS_FILE keeps generations locally when Agent Observability is off; clear it, or clear the Agent Observability settings")
	}
	if otlp && !validEndpoint(c.Endpoint, c.LocalCollector()) {
		return errors.New("OTLP_ENDPOINT must be an HTTPS URL without credentials, query parameters, or fragments (plain HTTP is allowed only for an endpoint on localhost)")
	}
	for _, endpoint := range []string{c.GenerationEndpoint, c.APIEndpoint} {
		if endpoint != "" && !validEndpoint(endpoint, false) {
			return errors.New("AGENTO11Y_ENDPOINT and AGENTO11Y_API_ENDPOINT must be HTTPS URLs without credentials, query parameters, or fragments (Agent Observability is a Grafana Cloud service, so plain HTTP is not accepted)")
		}
	}
	return nil
}

// validEndpoint reports whether endpoint is an HTTPS URL, or a plain-HTTP one
// when allowHTTP, without credentials, a query, or a fragment.
func validEndpoint(endpoint string, allowHTTP bool) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && allowHTTP)
}

// LocalCollector reports whether OTLP goes to a plain-HTTP receiver on this
// machine (a Collector, or the local stack) rather than straight to Grafana
// Cloud. A host name with no dot, such as the lgtm service of the local stack
// when the game runs in a container beside it, counts: it can only name a
// machine on the local network, never a public one.
func (c Config) LocalCollector() bool {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "http" {
		return false
	}
	switch host := u.Hostname(); {
	case host == "localhost", host == "127.0.0.1", host == "::1":
		return true
	default:
		return host != "" && !strings.Contains(host, ".") && !strings.Contains(host, ":")
	}
}

// GenerationConfig is the agento11y client configuration for c: generation
// export over HTTP with Grafana Cloud basic auth, and the API that player
// ratings go to. Without Agent Observability settings, the export is off:
// the client still records each model call as a span with the gen_ai.*
// attributes and in the gen_ai.client.* metrics, but nothing leaves the
// process through it. logger receives the SDK's own export diagnostics.
func GenerationConfig(c Config, logger *log.Logger) agento11y.Config {
	cfg := agento11y.DefaultConfig()
	cfg.Logger = logger
	cfg.AgentName = Service
	cfg.AgentVersion = c.Version
	if !c.GenerationConfigured() {
		cfg.GenerationExport.Protocol = agento11y.GenerationExportProtocolNone
		return cfg
	}
	cfg.GenerationExport.Protocol = agento11y.GenerationExportProtocolHTTP
	cfg.GenerationExport.Endpoint = c.GenerationEndpoint
	cfg.GenerationExport.Auth = agento11y.AuthConfig{Mode: agento11y.ExportAuthModeBasic, TenantID: c.Instance, BasicPassword: c.Token}
	// The SDK takes the API host from this URL, so the generation endpoint
	// serves when no separate API endpoint is set.
	cfg.API.Endpoint = first(c.APIEndpoint, c.GenerationEndpoint)
	return cfg
}

// OTLPHeaders are the HTTP headers for c's OTLP exporters: none for a local
// Collector without OTLP_HEADERS, otherwise Basic auth.
func (c Config) OTLPHeaders() map[string]string {
	if c.Authorization == "" {
		return nil
	}
	return map[string]string{"Authorization": c.Authorization}
}

type Runtime struct {
	// Client records model calls and tool executions. It is set whenever
	// telemetry is on; GenerationExport says whether it also sends them to
	// Agent Observability.
	Client           *agento11y.Client
	GenerationExport bool
	Logger           *slog.Logger
	shutdown         []func(context.Context) error
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	var errs []error
	if r.Client != nil {
		errs = append(errs, r.Client.Shutdown(ctx))
	}
	for i := len(r.shutdown) - 1; i >= 0; i-- {
		errs = append(errs, r.shutdown[i](ctx))
	}
	return errors.Join(errs...)
}

// Init sets up exporters. diag receives the local copy of asynchronous export
// diagnostics (see Diagnostics); pass os.Stderr for straight-through output.
func Init(ctx context.Context, c Config, diag io.Writer) (_ *Runtime, err error) {
	if err = c.Validate(); err != nil {
		return nil, err
	}
	r := &Runtime{}
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = r.Shutdown(cleanup)
		}
	}()
	r.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	logHandlers := fanOutHandler{}
	if c.OTLPConfigured() {
		if err = r.startOTLP(ctx, c, diag); err != nil {
			return nil, err
		}
		logHandlers = append(logHandlers, r.Logger.Handler())
	}
	// agento11y logs its own export failures (rejected/failed generation
	// sends, flush errors) through this logger rather than returning them to
	// the caller. Fan it out to diag in addition to r.Logger's normal Loki
	// destination, so those diagnostics are visible locally in real time
	// instead of requiring a round trip through Grafana Cloud to discover.
	// Routine per-batch success lines still go to Loki but not to diag.
	local := dropMessages{slog.NewTextHandler(diag, nil), sdkSuccessPrefixes}
	logHandlers = append(logHandlers, local)
	// The client is created without Agent Observability settings too: it
	// then only records each model call as a span and in the gen_ai.client.*
	// metrics, which is what the local stack shows.
	r.GenerationExport = c.GenerationConfigured()
	cfg := GenerationConfig(c, slog.NewLogLogger(logHandlers, slog.LevelInfo))
	if c.GenerationsFile != "" {
		keep, e := startGenerationLog(c.GenerationsFile)
		if e != nil {
			return nil, fmt.Errorf("ASIMOV_GENERATIONS_FILE: %w", e)
		}
		r.shutdown = append(r.shutdown, keep.Shutdown)
		cfg.GenerationExport.Protocol = agento11y.GenerationExportProtocolHTTP
		cfg.GenerationExport.Endpoint = keep.Endpoint()
		cfg.GenerationExport.Insecure = agento11y.BoolPtr(true)
	}
	r.Client = agento11y.NewClient(cfg)
	return r, nil
}

// instanceID is a random ID for this process.
func instanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startOTLP sets up the trace, metric, and log exporters for c's OTLP
// endpoint and installs them as the process's providers.
func (r *Runtime) startOTLP(ctx context.Context, c Config, diag io.Writer) error {
	// Each run gets its own instance ID, so its counters are separate series
	// instead of one that resets (and loses its first increment) every run.
	res := resource.NewSchemaless(attribute.String("service.name", Service), attribute.String("service.version", c.Version), attribute.String("service.instance.id", instanceID()))
	headers := c.OTLPHeaders()
	base := strings.TrimRight(c.Endpoint, "/")
	te, e := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(base+"/v1/traces"), otlptracehttp.WithHeaders(headers))
	if e != nil {
		return e
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(te), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	r.shutdown = append(r.shutdown, tp.Shutdown)
	me, e := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(base+"/v1/metrics"), otlpmetrichttp.WithHeaders(headers))
	if e != nil {
		return e
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(5*time.Second))))
	r.shutdown = append(r.shutdown, mp.Shutdown)
	le, e := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(base+"/v1/logs"), otlploghttp.WithHeaders(headers))
	if e != nil {
		return e
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(le)))
	r.shutdown = append(r.shutdown, lp.Shutdown)
	otel.SetTracerProvider(tp)
	// The OTel SDK's default error handler writes batch export failures to
	// stderr from background goroutines; send them to diag instead.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		fmt.Fprintln(diag, "telemetry export error:", err)
	}))
	otel.SetMeterProvider(mp)
	r.Logger = slog.New(otelslog.NewHandler(Service, otelslog.WithLoggerProvider(lp)))
	return nil
}

// sdkSuccessPrefixes are agento11y's routine per-export success messages. The
// SDK logs these at the same level as its failures, so they're filtered by
// message rather than level.
var sdkSuccessPrefixes = []string{
	"agento11y generation export response",
	"agento11y workflow step export response",
}

// dropMessages wraps a handler, discarding records whose message starts with
// any of the given prefixes.
type dropMessages struct {
	slog.Handler
	prefixes []string
}

func (d dropMessages) Handle(ctx context.Context, record slog.Record) error {
	for _, p := range d.prefixes {
		if strings.HasPrefix(record.Message, p) {
			return nil
		}
	}
	return d.Handler.Handle(ctx, record)
}

func (d dropMessages) WithAttrs(attrs []slog.Attr) slog.Handler {
	return dropMessages{d.Handler.WithAttrs(attrs), d.prefixes}
}

func (d dropMessages) WithGroup(name string) slog.Handler {
	return dropMessages{d.Handler.WithGroup(name), d.prefixes}
}

// fanOutHandler dispatches every record to each of its handlers in order,
// joining their errors. It exists only to give a single slog.Logger two
// destinations (here: the existing OTel/Loki handler plus stderr).
type fanOutHandler []slog.Handler

func (f fanOutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f fanOutHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, record.Level) {
			errs = append(errs, h.Handle(ctx, record.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanOutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanOutHandler, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanOutHandler) WithGroup(name string) slog.Handler {
	out := make(fanOutHandler, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
