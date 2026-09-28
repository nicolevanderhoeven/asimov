package telemetry

import (
	"context"
	"errors"
	"fmt"
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

type Config struct{ Endpoint, Authorization, GenerationEndpoint, Instance, Token, Version string }

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
		Instance:           first(os.Getenv("GRAFANA_CLOUD_INSTANCE_ID"), os.Getenv("GRAFANA_CLOUD_INSTANCE")),
		Token:              os.Getenv("GRAFANA_CLOUD_API_KEY"), Version: first(os.Getenv("ASIMOV_AGENT_VERSION"), "go-experiment-v1"),
	}
}

func (c Config) Validate() error {
	for name, v := range map[string]string{"OTLP_ENDPOINT": c.Endpoint, "OTLP_HEADERS": c.Authorization, "AGENTO11Y_ENDPOINT (or GRAFANA_CLOUD_SIGIL_ENDPOINT)": c.GenerationEndpoint, "GRAFANA_CLOUD_INSTANCE_ID": c.Instance, "GRAFANA_CLOUD_API_KEY": c.Token} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("missing %s; configure Grafana telemetry or explicitly use --no-telemetry", name)
		}
	}
	for _, endpoint := range []string{c.Endpoint, c.GenerationEndpoint} {
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

func Init(ctx context.Context, c Config) (_ *Runtime, err error) {
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
	otel.SetMeterProvider(mp)
	r.Logger = slog.New(otelslog.NewHandler(Service, otelslog.WithLoggerProvider(lp)))
	cfg := agento11y.DefaultConfig()
	cfg.Logger = slog.NewLogLogger(r.Logger.Handler(), slog.LevelInfo)
	cfg.AgentName = Service
	cfg.AgentVersion = c.Version
	cfg.GenerationExport.Protocol = agento11y.GenerationExportProtocolHTTP
	cfg.GenerationExport.Endpoint = c.GenerationEndpoint
	cfg.GenerationExport.Auth = agento11y.AuthConfig{Mode: agento11y.ExportAuthModeBasic, TenantID: c.Instance, BasicPassword: c.Token}
	r.Client = agento11y.NewClient(cfg)
	return r, nil
}
