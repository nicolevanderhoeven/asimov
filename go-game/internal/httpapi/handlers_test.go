package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
)

// fakeModel is a minimal provider.LanguageModel, just enough to drive
// gm.GM.Resolve/Narrate in tests without a network call.
type fakeModel struct {
	calls  []string // JSON-encoded game.Action tool-call args
	fail   bool
	params provider.CallOptions // set by the most recent DoGenerate/DoStream call
}

// toolCall splits a scripted call into its tool name and JSON arguments. A
// call is either bare JSON, for resolve_action, or "tool_name {json}".
func toolCall(c string) (string, string) {
	if name, input, ok := strings.Cut(c, " "); ok && !strings.HasPrefix(c, "{") {
		return name, input
	}
	return "resolve_action", c
}

func (*fakeModel) SpecificationVersion() string               { return "v4" }
func (*fakeModel) Provider() string                           { return "anthropic" }
func (*fakeModel) ModelID() string                            { return "test-model" }
func (*fakeModel) SupportedURLs() map[string][]*regexp.Regexp { return nil }

func (m *fakeModel) DoGenerate(_ context.Context, p provider.CallOptions) (*provider.GenerateResult, error) {
	m.params = p
	if m.fail {
		return nil, errors.New("model unavailable")
	}
	r := &provider.GenerateResult{FinishReason: provider.FinishReason{Unified: provider.FinishReasonToolCalls}}
	for i, c := range m.calls {
		tool, input := toolCall(c)
		r.Content = append(r.Content, provider.GenerateContentPart{Type: provider.ContentToolCall, ToolCallID: string(rune('a' + i)), ToolName: tool, Input: json.RawMessage(input)})
	}
	return r, nil
}

func (m *fakeModel) DoStream(_ context.Context, p provider.CallOptions) (*provider.StreamResult, error) {
	m.params = p
	if m.fail {
		return nil, errors.New("model unavailable")
	}
	c := make(chan provider.StreamPart, len(m.calls)+4)
	// Narration offers roll_dice; this fake narrates without rolling.
	narrating := slices.ContainsFunc(p.Tools, func(t provider.Tool) bool { return t.Name == "roll_dice" })
	if len(p.Tools) > 0 && !narrating {
		for i, input := range m.calls {
			tool, input := toolCall(input)
			c <- provider.StreamPart{Type: provider.PartToolCall, ToolCallID: string(rune('a' + i)), ToolName: tool, Input: input}
		}
		c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: provider.FinishReasonToolCalls}}
		close(c)
		return &provider.StreamResult{Stream: c}, nil
	}
	c <- provider.StreamPart{Type: provider.PartTextStart, ID: "text"}
	c <- provider.StreamPart{Type: provider.PartTextDelta, ID: "text", Delta: "The console reveals a diagnostic entry."}
	c <- provider.StreamPart{Type: provider.PartTextEnd, ID: "text"}
	c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: provider.FinishReasonStop}}
	close(c)
	return &provider.StreamResult{Stream: c}, nil
}

func newTestServer(t *testing.T, model provider.LanguageModel) *httptest.Server {
	t.Helper()
	g := &gm.GM{Model: model, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Roll: func(int) int { return 15 }}
	srv := NewServer(g, NewStore(time.Minute), g.Logger, model == nil)
	return httptest.NewServer(srv.Handler())
}

func createSession(t *testing.T, base string) string {
	t.Helper()
	res, err := http.Post(base+"/session", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /session = %d", res.StatusCode)
	}
	var body sessionResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.SessionID
}

func TestCreateSessionReturnsInitialView(t *testing.T) {
	ts := newTestServer(t, &fakeModel{})
	defer ts.Close()
	id := createSession(t, ts.URL)
	if id == "" {
		t.Fatal("empty session id")
	}
}

func TestGetUnknownSessionIs404(t *testing.T) {
	ts := newTestServer(t, &fakeModel{})
	defer ts.Close()
	res, err := http.Get(ts.URL + "/session/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET unknown session = %d, want 404", res.StatusCode)
	}
}

func TestActionDeterministicInspect(t *testing.T) {
	ts := newTestServer(t, &fakeModel{})
	defer ts.Close()
	id := createSession(t, ts.URL)
	res, err := http.Post(ts.URL+"/session/"+id+"/actions", "application/json", bytes.NewBufferString(`{"kind":"inspect","target":"logs"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST actions = %d, want 200", res.StatusCode)
	}
	var result game.Result
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Allowed || result.State.Turn != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestActionMissingFieldsIs400(t *testing.T) {
	ts := newTestServer(t, &fakeModel{})
	defer ts.Close()
	id := createSession(t, ts.URL)
	for _, body := range []string{`{"kind":"inspect"}`, `{"target":"logs"}`, `{not valid json`} {
		res, err := http.Post(ts.URL+"/session/"+id+"/actions", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, res.StatusCode)
		}
	}
}

func TestActionUnknownSessionIs404(t *testing.T) {
	ts := newTestServer(t, &fakeModel{})
	defer ts.Close()
	res, err := http.Post(ts.URL+"/session/does-not-exist/actions", "application/json", bytes.NewBufferString(`{"kind":"inspect","target":"logs"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestActionAfterEndedSessionIs409(t *testing.T) {
	ts := newTestServer(t, &fakeModel{})
	defer ts.Close()
	id := createSession(t, ts.URL)
	// Drive the session to a won state directly via /actions using the same
	// deterministic sequence game_test.go uses for TestCompleteRescueWithoutCombat.
	sequence := []string{
		`{"kind":"inspect","target":"logs"}`, `{"kind":"scan","target":"sensors"}`,
		`{"kind":"move","target":"sickbay"}`, `{"kind":"inspect","target":"medical_records"}`,
		`{"kind":"move","target":"bridge"}`, `{"kind":"move","target":"engineering"}`,
		`{"kind":"inspect","target":"relay"}`, `{"kind":"bypass","target":"drone"}`,
		`{"kind":"isolate","target":"relay"}`, `{"kind":"rescue","target":"crew"}`,
	}
	for _, body := range sequence {
		res, err := http.Post(ts.URL+"/session/"+id+"/actions", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		var result game.Result
		err = json.NewDecoder(res.Body).Decode(&result)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || err != nil {
			t.Fatalf("action %s: status = %d, want 200 (%v)", body, res.StatusCode, err)
		}
		if result.RollRequired != nil {
			if status, _ := postRoll(t, ts.URL, id, `{"ability":"`+result.RollRequired.Ability+`"}`); status != http.StatusOK {
				t.Fatalf("roll for %s: status = %d, want 200", body, status)
			}
		}
	}
	res, err := http.Post(ts.URL+"/session/"+id+"/actions", "application/json", bytes.NewBufferString(`{"kind":"inspect","target":"logs"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("action after ending: status = %d, want 409", res.StatusCode)
	}
}

func TestResolveOfflineIs503(t *testing.T) {
	ts := newTestServer(t, nil)
	defer ts.Close()
	id := createSession(t, ts.URL)
	res, err := http.Post(ts.URL+"/session/"+id+"/resolve", "application/json", bytes.NewBufferString(`{"input":"Read the logs"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.StatusCode)
	}
}

func TestResolveSuccess(t *testing.T) {
	ts := newTestServer(t, &fakeModel{calls: []string{`{"kind":"inspect","target":"logs"}`}})
	defer ts.Close()
	id := createSession(t, ts.URL)
	res, err := http.Post(ts.URL+"/session/"+id+"/resolve", "application/json", bytes.NewBufferString(`{"input":"Read the logs"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body resolveResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Result.Allowed || body.Narration == "" {
		t.Fatalf("unexpected resolve response: %+v", body)
	}
}

func TestResolveModelFailureIs422(t *testing.T) {
	ts := newTestServer(t, &fakeModel{calls: []string{`{"kind":"inspect","target":"logs"}`, `{"kind":"move","target":"sickbay"}`}})
	defer ts.Close()
	id := createSession(t, ts.URL)
	res, err := http.Post(ts.URL+"/session/"+id+"/resolve", "application/json", bytes.NewBufferString(`{"input":"Do two things"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", res.StatusCode)
	}
	// State must be unchanged: the session should still be on turn 0.
	v, ok := st(id, ts)
	if !ok || v.Turn != 0 {
		t.Fatalf("state changed after a rejected resolve: %+v", v)
	}
}

func TestResolveHistoryAccumulatesAcrossTurns(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"inspect","target":"logs"}`}}
	ts := newTestServer(t, m)
	defer ts.Close()
	id := createSession(t, ts.URL)

	res, err := http.Post(ts.URL+"/session/"+id+"/resolve", "application/json", bytes.NewBufferString(`{"input":"Read the logs"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("first resolve status = %d, want 200", res.StatusCode)
	}
	// The very first turn has no prior history to replay: system + input only.
	if got := len(m.params.Prompt); got != 2 {
		t.Fatalf("first turn prompt length = %d, want 2: %+v", got, m.params.Prompt)
	}

	res, err = http.Post(ts.URL+"/session/"+id+"/resolve", "application/json", bytes.NewBufferString(`{"input":"Take the turbolift to engineering"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("second resolve status = %d, want 200", res.StatusCode)
	}
	// The second turn must replay the first turn's (user, assistant) pair
	// ahead of its own input: system + 2 history + input = 4.
	if got := len(m.params.Prompt); got != 4 {
		t.Fatalf("second turn prompt length = %d, want 4: %+v", got, m.params.Prompt)
	}
	if m.params.Prompt[1].Role != provider.RoleUser || m.params.Prompt[2].Role != provider.RoleAssistant {
		t.Fatalf("replayed history out of order: %+v", m.params.Prompt)
	}
}

func postRoll(t *testing.T, base, id, body string) (int, resolveResponse) {
	t.Helper()
	res, err := http.Post(base+"/session/"+id+"/roll", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out resolveResponse
	if res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode, out
}

func TestRollResolvesPendingAction(t *testing.T) {
	ts := newTestServer(t, &fakeModel{})
	defer ts.Close()
	id := createSession(t, ts.URL)
	res, err := http.Post(ts.URL+"/session/"+id+"/actions", "application/json", bytes.NewBufferString(`{"kind":"scan","target":"sensors"}`))
	if err != nil {
		t.Fatal(err)
	}
	var pending game.Result
	err = json.NewDecoder(res.Body).Decode(&pending)
	res.Body.Close()
	if err != nil || pending.RollRequired == nil || pending.State.Turn != 0 {
		t.Fatalf("scan should wait for a roll: %+v %v", pending, err)
	}
	if status, _ := postRoll(t, ts.URL, id, `{}`); status != http.StatusBadRequest {
		t.Fatalf("missing ability: status = %d, want 400", status)
	}
	status, body := postRoll(t, ts.URL, id, `{"ability":"Intelligence","narrate":true}`)
	if status != http.StatusOK || !body.Result.Allowed || body.Result.State.Turn != 1 || len(body.Result.Rolls) != 1 || body.Result.Rolls[0].By != game.ByPlayer || body.Narration == "" {
		t.Fatalf("unexpected roll response: %d %+v", status, body)
	}
}

func TestResolveRoutesRollCommandToEngine(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"scan","target":"sensors"}`}}
	ts := newTestServer(t, m)
	defer ts.Close()
	id := createSession(t, ts.URL)
	for i, input := range []string{"Scan the sensor buffer", "/roll Intelligence"} {
		res, err := http.Post(ts.URL+"/session/"+id+"/resolve", "application/json", bytes.NewBufferString(`{"input":"`+input+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		var body resolveResponse
		err = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("%q: status %d %v", input, res.StatusCode, err)
		}
		if (i == 0) != (body.Result.RollRequired != nil) || body.Result.State.Turn != i {
			t.Fatalf("%q: unexpected result %+v", input, body.Result)
		}
	}
}

func TestImproviseThenRoll(t *testing.T) {
	ts := newTestServer(t, nil)
	defer ts.Close()
	id := createSession(t, ts.URL)
	res, err := http.Post(ts.URL+"/session/"+id+"/improvise", "application/json", bytes.NewBufferString(`{"approach":"reroute the buffer","ability":"int","skill":"investigation","difficulty":"easy","effect":"recover_frequency"}`))
	if err != nil {
		t.Fatal(err)
	}
	var pending game.Result
	err = json.NewDecoder(res.Body).Decode(&pending)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK || pending.RollRequired == nil || pending.RollRequired.Target != 15 {
		t.Fatalf("improvise: %d %+v %v", res.StatusCode, pending, err)
	}
	// The fixed roller rolls 15; +6 investigation beats DC 15.
	status, body := postRoll(t, ts.URL, id, `{"ability":"Intelligence"}`)
	if status != http.StatusOK || !body.Result.Allowed || body.Result.State.Turn != 1 || len(body.Result.State.Discovered) != 1 {
		t.Fatalf("roll: %d %+v", status, body)
	}
	res, err = http.Post(ts.URL+"/session/"+id+"/improvise", "application/json", bytes.NewBufferString(`{"ability":"int"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing fields: status = %d, want 400", res.StatusCode)
	}
}

func TestNarratedRollOfflineIs503(t *testing.T) {
	ts := newTestServer(t, nil)
	defer ts.Close()
	id := createSession(t, ts.URL)
	if status, _ := postRoll(t, ts.URL, id, `{"ability":"Intelligence","narrate":true}`); status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
}

func st(id string, ts *httptest.Server) (game.View, bool) {
	res, err := http.Get(ts.URL + "/session/" + id)
	if err != nil {
		return game.View{}, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return game.View{}, false
	}
	var body sessionResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return game.View{}, false
	}
	return body.State, true
}
