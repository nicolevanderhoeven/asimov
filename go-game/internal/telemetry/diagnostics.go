package telemetry

import (
	"bytes"
	"io"
	"sync"
)

// Diagnostics is the local destination for asynchronous telemetry messages
// (export failures, rejected generations, OTel SDK errors). Those arrive on
// background goroutines at arbitrary times, so an interactive caller can Hold
// them and Flush at a point where printing won't land on top of its input
// prompt. When not held, writes pass straight through.
type Diagnostics struct {
	mu   sync.Mutex
	out  io.Writer
	held bool
	buf  bytes.Buffer
}

func NewDiagnostics(out io.Writer) *Diagnostics { return &Diagnostics{out: out} }

func (d *Diagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.held {
		return d.buf.Write(p)
	}
	return d.out.Write(p)
}

// Hold starts buffering writes until Flush or Release.
func (d *Diagnostics) Hold() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.held = true
}

// Flush writes out anything buffered so far, leaving the hold in place.
func (d *Diagnostics) Flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.buf.Len() > 0 {
		_, _ = d.buf.WriteTo(d.out)
	}
}

// Release flushes and returns to writing straight through.
func (d *Diagnostics) Release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.buf.Len() > 0 {
		_, _ = d.buf.WriteTo(d.out)
	}
	d.held = false
}
