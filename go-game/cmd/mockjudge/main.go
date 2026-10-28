// Command mockjudge runs a local stand-in for the Anthropic Messages API, so
// the k6 tests can run with no API key: set ANTHROPIC_API_URL to its
// /v1/messages address. See internal/mockjudge for what it answers.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/mockjudge"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "Listen address; defaults to $ASIMOV_MOCK_JUDGE_ADDR when set")
	flag.Parse()
	if v := os.Getenv("ASIMOV_MOCK_JUDGE_ADDR"); v != "" && *addr == "127.0.0.1:8099" {
		*addr = v
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	srv := &http.Server{Handler: mockjudge.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	fmt.Printf("Mock judge listening on http://%s/v1/messages (set ANTHROPIC_API_URL to this)\n", ln.Addr())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
