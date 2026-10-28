// Package mockjudge is a small local stand-in for the Anthropic Messages API,
// for the k6 tests: point ANTHROPIC_API_URL at it and the whole suite runs
// with no API key and no cost.
//
// The k6 scripts call Claude in five roles: the probe generator, the judges,
// the e2e player, its persona, and the story critic. Each call asks for JSON
// matching a schema, so the server tells the roles apart by the schema's
// fields and answers each one the way a competent Claude would, from the
// evidence in the request alone. The judges reason from the transcript they
// are given (the narration, and the engine state beside it), not from any
// marker the mock game master leaves: a flaw is caught because the narration
// contradicts the engine, the same way a real judge would see it.
package mockjudge

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Handler serves the Messages API subset the k6 scripts use: POST
// /v1/messages with an output_config JSON schema.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", handleMessages)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok\n") })
	return mux
}

// request is the part of a Messages API request the mock reads.
type request struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	System    any    `json:"system"`
	Messages  []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
	OutputConfig struct {
		Format struct {
			Type   string         `json:"type"`
			Schema map[string]any `json:"schema"`
		} `json:"format"`
	} `json:"output_config"`
}

// system is the system prompt as text; it may be a string or text blocks.
func (r request) system() string { return textOf(r.System) }

// user is the first user message's text, which holds the evidence; last is
// the final user message's.
func (r request) user() string {
	for _, m := range r.Messages {
		if m.Role == "user" {
			return textOf(m.Content)
		}
	}
	return ""
}

func (r request) last() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return textOf(r.Messages[i].Content)
		}
	}
	return ""
}

func textOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, part := range x {
			if m, ok := part.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	}
	return ""
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		return
	}
	props, _ := req.OutputConfig.Format.Schema["properties"].(map[string]any)
	if len(props) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "the mock judge answers only requests with an output_config JSON schema")
		return
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	role, ok := roles[strings.Join(keys, ",")]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("the mock judge doesn't know a request whose schema has the fields %s", strings.Join(keys, ", ")))
		return
	}
	answer, err := role(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	text, _ := json.Marshal(answer)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            "msg_mock",
		"type":          "message",
		"role":          "assistant",
		"model":         req.Model,
		"content":       []map[string]string{{"type": "text", "text": string(text)}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         map[string]int{"input_tokens": len(req.user())/4 + 1, "output_tokens": len(text)/4 + 1},
	})
}

// roles maps a request's schema fields to the role that answers it.
var roles = map[string]func(request) (any, error){
	"input":                        probeInput,
	"input,intent":                 playerMove,
	"name,style":                   persona,
	"pass,probe_valid,reason":      probeVerdict,
	"pass,reason":                  outputVerdict,
	"quote,reports_roll":           rollReport,
	"defensible,reason,would_roll": rulingVerdict,
	"categories,ending_cause,ending_reason,findings,intents": runVerdict,
	"critique,dimensions,highlights,lowlights,overall":       storyVerdict,
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, kind, msg string) {
	writeJSON(w, status, map[string]any{"type": "error", "error": map[string]string{"type": kind, "message": msg}})
}
