package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"testing"

	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/dicegm"
)

// rollingModel asks for one roll on its first call of each turn, then
// narrates a number regardless of what the roll returned.
type rollingModel struct{ calls int }

func (*rollingModel) SpecificationVersion() string               { return "v4" }
func (*rollingModel) Provider() string                           { return "anthropic" }
func (*rollingModel) ModelID() string                            { return "test-model" }
func (*rollingModel) SupportedURLs() map[string][]*regexp.Regexp { return nil }
func (*rollingModel) DoGenerate(context.Context, provider.CallOptions) (*provider.GenerateResult, error) {
	return nil, errors.New("not used")
}
func (m *rollingModel) DoStream(_ context.Context, p provider.CallOptions) (*provider.StreamResult, error) {
	m.calls++
	c := make(chan provider.StreamPart, 5)
	last := p.Prompt[len(p.Prompt)-1]
	if last.Role == provider.RoleUser {
		c <- provider.StreamPart{Type: provider.PartToolCall, ToolCallID: "call1", ToolName: dicegm.ToolName, Input: `{"notation":"1d20","reason":"lock"}`}
		c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: provider.FinishReasonToolCalls}}
	} else {
		c <- provider.StreamPart{Type: provider.PartTextStart, ID: "t"}
		c <- provider.StreamPart{Type: provider.PartTextDelta, ID: "t", Delta: "You roll a 20. The lock opens."}
		c <- provider.StreamPart{Type: provider.PartTextEnd, ID: "t"}
		c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: provider.FinishReasonStop}}
	}
	close(c)
	return &provider.StreamResult{Stream: c}, nil
}

func postDM(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	res, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	return res.StatusCode, buf.Bytes()
}

func TestDMTurnReturnsTrajectory(t *testing.T) {
	ts := newTestServer(t, &rollingModel{})
	defer ts.Close()
	status, body := postDM(t, ts.URL+"/dm", "")
	var created dmSessionResponse
	if status != http.StatusCreated || json.Unmarshal(body, &created) != nil || created.SessionID == "" {
		t.Fatalf("POST /dm = %d %s", status, body)
	}
	for want := 1; want <= 2; want++ {
		status, body = postDM(t, ts.URL+"/dm/"+created.SessionID+"/turns", `{"input":"I pick the lock"}`)
		var turn dicegm.Turn
		if status != http.StatusOK || json.Unmarshal(body, &turn) != nil {
			t.Fatalf("POST turns = %d %s", status, body)
		}
		// The narration's 20 is returned as written, next to the real roll.
		if turn.Turn != want || len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Result == nil || turn.Narration != "You roll a 20. The lock opens." || len(turn.Steps) != 2 {
			t.Fatalf("turn %d: %s", want, body)
		}
	}
}

func TestDMErrors(t *testing.T) {
	ts := newTestServer(t, &rollingModel{})
	defer ts.Close()
	if status, _ := postDM(t, ts.URL+"/dm/nope/turns", `{"input":"hi"}`); status != http.StatusNotFound {
		t.Error("unknown session", status)
	}
	_, body := postDM(t, ts.URL+"/dm", "")
	var created dmSessionResponse
	json.Unmarshal(body, &created)
	if status, _ := postDM(t, ts.URL+"/dm/"+created.SessionID+"/turns", `{}`); status != http.StatusBadRequest {
		t.Error("missing input", status)
	}
	offline := newTestServer(t, nil)
	defer offline.Close()
	if status, _ := postDM(t, offline.URL+"/dm", ""); status != http.StatusServiceUnavailable {
		t.Error("offline", status)
	}
}
