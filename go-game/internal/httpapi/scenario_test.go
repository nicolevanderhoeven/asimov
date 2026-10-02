package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
)

func postSession(t *testing.T, base, body string) (int, sessionResponse) {
	t.Helper()
	res, err := http.Post(base+"/session", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out sessionResponse
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestCreateSessionChoosesTheScenario(t *testing.T) {
	srv := newTestServer(t, nil)
	defer srv.Close()
	if code, s := postSession(t, srv.URL, ""); code != http.StatusCreated || s.Scenario.Mode != "classic" || s.Scenario.ID != game.ClassicID {
		t.Fatalf("default: %d %+v", code, s.Scenario)
	}
	want := game.Generate(99)
	code, s := postSession(t, srv.URL, `{"scenario":"generated","seed":99}`)
	if code != http.StatusCreated || s.Scenario.Mode != "generated" || s.Scenario.Variant != want.Variant() || s.Scenario.Seed != 99 || s.State.Encounter == nil {
		t.Fatalf("generated: %d %+v", code, s.Scenario)
	}
	if _, again := postSession(t, srv.URL, `{"seed":99}`); again.Scenario.Variant != want.Variant() {
		t.Fatal("a seed alone should replay the generated scenario")
	}
	if _, random := postSession(t, srv.URL, `{"scenario":"generated"}`); random.Scenario.Seed == 0 || random.Scenario.Seed >= 1<<53 {
		t.Fatalf("a random generated scenario should report a seed JavaScript can hold exactly: %d", random.Scenario.Seed)
	}
	for _, bad := range []string{`{"scenario":"bogus"}`, `{"scenario":"classic","seed":3}`, `{`} {
		if code, _ := postSession(t, srv.URL, bad); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
}

func TestScenarioEndpointReturnsTheSolution(t *testing.T) {
	srv := newTestServer(t, nil)
	defer srv.Close()
	_, s := postSession(t, srv.URL, `{"seed":5}`)
	res, err := http.Get(srv.URL + "/session/" + s.SessionID + "/scenario")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var sc game.Scenario
	if err := json.NewDecoder(res.Body).Decode(&sc); err != nil || res.StatusCode != http.StatusOK || sc.Summary != game.Generate(5).Summary || len(sc.Clues) != 4 {
		t.Fatalf("%d %v %+v", res.StatusCode, err, sc)
	}
	if res, _ := http.Get(srv.URL + "/session/nope/scenario"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown session: %d", res.StatusCode)
	}
}

func TestServerDefaultScenario(t *testing.T) {
	g := &gm.GM{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Roll: func(int) int { return 15 }}
	api := NewServer(g, NewStore(time.Minute), g.Logger, true)
	api.DefaultScenario = "generated"
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	if _, s := postSession(t, srv.URL, ""); s.Scenario.Mode != "generated" {
		t.Fatalf("%+v", s.Scenario)
	}
	if _, s := postSession(t, srv.URL, `{"scenario":"classic"}`); s.Scenario.Mode != "classic" {
		t.Fatalf("%+v", s.Scenario)
	}
}
