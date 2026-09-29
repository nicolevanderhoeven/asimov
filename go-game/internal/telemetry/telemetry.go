package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
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

// APIEndpoint is the Agent Observability API base, used for experiments and
// scores (not generation export). It defaults to the generation endpoint's
// scheme and host, where Grafana Cloud serves both.
type Config struct{ Endpoint, Authorization, GenerationEndpoint, APIEndpoint, Instance, Token, Version string }

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
	generation := first(os.Getenv("AGENTO11Y_ENDPOINT"), os.Getenv("GRAFANA_CLOUD_SIGIL_ENDPOINT"))
	return Config{
		Endpoint: os.Getenv("OTLP_ENDPOINT"), Authorization: auth,
		GenerationEndpoint: generation,
		APIEndpoint:        first(os.Getenv("AGENTO11Y_API_ENDPOINT"), apiBase(generation)),
		Instance:           first(os.Getenv("GRAFANA_CLOUD_INSTANCE_ID"), os.Getenv("GRAFANA_CLOUD_INSTANCE")),
		Token:              os.Getenv("GRAFANA_CLOUD_API_KEY"), Version: first(os.Getenv("ASIMOV_AGENT_VERSION"), "go-experiment-v1"),
	}
}

// apiBase returns endpoint's scheme and host, or "" if it doesn't parse.
func apiBase(endpoint string) string {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (c Config) Validate() error {
	for name, v := range map[string]string{"OTLP_ENDPOINT": c.Endpoint, "OTLP_HEADERS": c.Authorization, "AGENTO11Y_ENDPOINT (or GRAFANA_CLOUD_SIGIL_ENDPOINT)": c.GenerationEndpoint, "GRAFANA_CLOUD_INSTANCE_ID": c.Instance, "GRAFANA_CLOUD_API_KEY": c.Token} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("missing %s; configure Grafana telemetry or explicitly use --no-telemetry", name)
		}
	}
	endpoints := []string{c.Endpoint, c.GenerationEndpoint}
	if c.APIEndpoint != "" {
		endpoints = append(endpoints, c.APIEndpoint)
	}
	for _, endpoint := range endpoints {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("telemetry endpoints must be HTTPS URLs without credentials, query parameters, or fragments")
		}
	}
	return nil
}

type Runtime struct {
	Client   *agento11y.Client
	Logger   *slog.Logger
	shutdown []func(context.Context) error
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
	res := resource.NewSchemaless(attribute.String("service.name", Service), attribute.String("service.version", c.Version))
	headers := map[string]string{"Authorization": c.Authorization}
	base := strings.TrimRight(c.Endpoint, "/")
	te, e := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(base+"/v1/traces"), otlptracehttp.WithHeaders(headers))
	if e != nil {
		return nil, e
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(te), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	r.shutdown = append(r.shutdown, tp.Shutdown)
	me, e := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(base+"/v1/metrics"), otlpmetrichttp.WithHeaders(headers))
	if e != nil {
		return nil, e
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(5*time.Second))))
	r.shutdown = append(r.shutdown, mp.Shutdown)
	le, e := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(base+"/v1/logs"), otlploghttp.WithHeaders(headers))
	if e != nil {
		return nil, e
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
	cfg := agento11y.DefaultConfig()
	// agento11y logs its own export failures (rejected/failed generation
	// sends, flush errors) through this logger rather than returning them to
	// the caller. Fan it out to diag in addition to r.Logger's normal Loki
	// destination, so those diagnostics are visible locally in real time
	// instead of requiring a round trip through Grafana Cloud to discover.
	// Routine per-batch success lines still go to Loki but not to diag.
	local := dropMessages{slog.NewTextHandler(diag, nil), sdkSuccessPrefixes}
	cfg.Logger = slog.NewLogLogger(fanOutHandler{r.Logger.Handler(), local}, slog.LevelInfo)
	cfg.AgentName = Service
	cfg.AgentVersion = c.Version
	cfg.GenerationExport.Protocol = agento11y.GenerationExportProtocolHTTP
	cfg.GenerationExport.Endpoint = c.GenerationEndpoint
	if c.APIEndpoint != "" {
		cfg.API.Endpoint = c.APIEndpoint
	}
	cfg.GenerationExport.Auth = agento11y.AuthConfig{Mode: agento11y.ExportAuthModeBasic, TenantID: c.Instance, BasicPassword: c.Token}
	r.Client = agento11y.NewClient(cfg)
	return r, nil
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
