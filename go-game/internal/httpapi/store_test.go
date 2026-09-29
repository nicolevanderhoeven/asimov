package httpapi

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/grafana/ai-sdk/provider"
)

func TestCreateAndView(t *testing.T) {
	st := NewStore(time.Minute)
	id, v := st.Create()
	if id == "" || v.Location != "bridge" {
		t.Fatalf("unexpected initial session: %q %+v", id, v)
	}
	got, ok := st.View(id)
	if !ok || got.Location != "bridge" {
		t.Fatalf("View() = %+v, %v", got, ok)
	}
	if _, ok := st.View("does-not-exist"); ok {
		t.Fatal("View() found a session that was never created")
	}
}

func TestWithSessionUnknownID(t *testing.T) {
	st := NewStore(time.Minute)
	if st.WithSession("does-not-exist", func(*SessionData) {}) {
		t.Fatal("WithSession() reported success for an unknown session")
	}
}

func TestWithSessionPersistsHistoryAcrossCalls(t *testing.T) {
	st := NewStore(time.Minute)
	id, _ := st.Create()
	st.WithSession(id, func(data *SessionData) {
		if len(data.History) != 0 {
			t.Fatalf("fresh session should start with no history: %+v", data.History)
		}
		data.History = append(data.History, provider.UserText("hello"))
	})
	st.WithSession(id, func(data *SessionData) {
		if len(data.History) != 1 {
			t.Fatalf("history did not persist across calls: %+v", data.History)
		}
	})
}

func TestConcurrentSessionsDoNotInterfere(t *testing.T) {
	st := NewStore(time.Minute)
	const n = 20
	ids := make([]string, n)
	for i := range ids {
		id, _ := st.Create()
		ids[i] = id
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				st.WithSession(id, func(data *SessionData) { data.State.Turn++ })
			}
		}(id)
	}
	wg.Wait()
	for _, id := range ids {
		v, ok := st.View(id)
		if !ok || v.Turn != 5 {
			t.Fatalf("session %s: turn=%d ok=%v, want 5", id, v.Turn, ok)
		}
	}
}

func TestSweepExpiresIdleSessions(t *testing.T) {
	st := NewStore(10 * time.Millisecond)
	id, _ := st.Create()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go st.Sweep(ctx, 5*time.Millisecond)
	// Sleep well past the TTL without touching the session — View() itself
	// refreshes last-access, so polling it here would defeat the test.
	time.Sleep(100 * time.Millisecond)
	if _, ok := st.View(id); ok {
		t.Fatal("session was not swept after exceeding its TTL")
	}
}
