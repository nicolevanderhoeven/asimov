package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agento11yv1 "github.com/grafana/agento11y/go/proto/agento11y/v1"
	"github.com/grafana/agento11y/go/proto/agento11y/wire"
	"google.golang.org/protobuf/encoding/protojson"
)

// generationLog stands in for Agent Observability's generation ingest when it
// isn't set up: a listener on this machine that the agento11y client exports
// to, and that appends each generation, with its prompts, messages, and tool
// calls, to a JSON-lines file. agento11y/judge-local.py judges that file.
type generationLog struct {
	listener net.Listener
	server   *http.Server
	mu       sync.Mutex
	file     *os.File
}

// startGenerationLog listens on a free loopback port and appends to path,
// creating its directory when needed.
func startGenerationLog(path string) (*generationLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	g := &generationLog{listener: ln, file: f}
	g.server = &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = g.server.Serve(ln) }()
	return g, nil
}

// Endpoint is the base URL for the client's GenerationExport.Endpoint.
func (g *generationLog) Endpoint() string { return "http://" + g.listener.Addr().String() }

// ServeHTTP accepts generation exports, and acknowledges workflow steps,
// which the game doesn't use, without keeping them.
func (g *generationLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", wire.ContentTypeJSON)
	if strings.Contains(r.URL.Path, "workflow") {
		_, _ = w.Write([]byte("{}"))
		return
	}
	req, err := wire.UnmarshalExportGenerationsJSON(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := &agento11yv1.ExportGenerationsResponse{}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, gen := range req.GetGenerations() {
		line, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(gen)
		if err == nil {
			// protojson output is randomly spaced; one generation per line
			// needs it compact.
			var compact bytes.Buffer
			if err = json.Compact(&compact, line); err == nil {
				compact.WriteByte('\n')
				_, err = g.file.Write(compact.Bytes())
			}
		}
		resp.Results = append(resp.Results, &agento11yv1.ExportGenerationResult{GenerationId: gen.GetId(), Accepted: err == nil})
	}
	out, err := wire.MarshalExportGenerationsResponseJSON(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(out)
}

// Shutdown stops the listener and closes the file.
func (g *generationLog) Shutdown(ctx context.Context) error {
	err := g.server.Shutdown(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	return errors.Join(err, g.file.Close())
}
