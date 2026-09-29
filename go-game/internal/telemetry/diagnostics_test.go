package telemetry

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestDiagnosticsHoldUntilFlush(t *testing.T) {
	var out bytes.Buffer
	d := NewDiagnostics(&out)
	fmt.Fprintln(d, "before hold")
	d.Hold()
	fmt.Fprintln(d, "while held")
	if out.String() != "before hold\n" {
		t.Fatalf("held write leaked: %q", out.String())
	}
	d.Flush()
	fmt.Fprintln(d, "still held")
	if out.String() != "before hold\nwhile held\n" {
		t.Fatalf("flush: %q", out.String())
	}
	d.Release()
	fmt.Fprintln(d, "released")
	if out.String() != "before hold\nwhile held\nstill held\nreleased\n" {
		t.Fatalf("release: %q", out.String())
	}
}

func TestSDKSuccessLinesDroppedLocally(t *testing.T) {
	var out bytes.Buffer
	// Same shape as Init's agento11y logger: a log.Logger whose Printf lines
	// become slog record messages.
	l := slog.NewLogLogger(dropMessages{slog.NewTextHandler(&out, nil), sdkSuccessPrefixes}, slog.LevelInfo)
	l.Printf("agento11y generation export response requested=%d results=%d", 1, 1)
	l.Printf("agento11y workflow step export response requested=%d results=%d", 1, 1)
	if out.Len() != 0 {
		t.Fatalf("success line printed: %q", out.String())
	}
	l.Printf("agento11y generation export failed: %v", "boom")
	if !strings.Contains(out.String(), "export failed: boom") {
		t.Fatalf("failure line dropped: %q", out.String())
	}
}
