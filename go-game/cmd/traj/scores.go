package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/dicegm"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
)

// scoreBatch is how many scores go in one export request.
const scoreBatch = 100

// experiment records one harness invocation as an Agent Observability
// experiment. Each turn's checks become scores on that turn's final
// generation (the one whose output is the narration), in the run's
// conversation, so they sit next to the generations they grade.
//
// It does not use trials, and leaves evaluator_kind out of each score
// (recording it in metadata instead). Agent Observability accepts trial
// writes only from the actor that created the experiment, identified by a
// source field on every write, and the Go SDK's trial requests (v0.15.0
// through v0.18.0) send no source, so they are refused ("experiment is owned
// by another actor"). The API also rejects evaluator_kind as a field ("invalid
// request body"). tests/test-trajectory.js calls the API directly with a
// source, so its runs do record trials.
type experiment struct {
	client     *agento11y.Client
	runID      string
	judgeModel string
	diag       io.Writer

	mu     sync.Mutex
	scores []agento11y.ScoreItem
}

func caseID(turn int) string { return fmt.Sprintf("turn-%d", turn) }

// startExperiment registers the experiment. It returns nil, and scores are
// skipped, when telemetry is off or registration fails; the harness itself
// still runs.
func startExperiment(ctx context.Context, client *agento11y.Client, model, judgeModel, version string, runs int, diag io.Writer) *experiment {
	if client == nil {
		return nil
	}
	runID := agento11y.StableID("exp", "dicegm", time.Now().UnixNano())
	_, err := client.CreateExperiment(ctx, agento11y.CreateExperimentRequest{
		RunID:       runID,
		Name:        "dice GM trajectory " + time.Now().UTC().Format(time.RFC3339),
		Description: fmt.Sprintf("%d runs of the %d-turn dice GM script, graded by trajeval", runs, len(script)),
		Tags:        []string{dicegm.Component, "trajectory"},
		Metadata: map[string]any{
			"suite_id": "dice-gm-script",
			// The suite version follows the script, so changing a player line
			// starts a new version rather than mixing results.
			"suite_version":  fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(script, "\n"))))[:12],
			"agent_name":     telemetry.Service,
			"agent_version":  version,
			"model_provider": "anthropic",
			"model_name":     model,
			"judge_model":    judgeModel,
			"runs":           runs,
			"max_iterations": dicegm.MaxIterations,
		},
	})
	if err != nil {
		fmt.Fprintln(diag, "Agent Observability experiment not started; scores will not be sent:", err)
		return nil
	}
	return &experiment{client: client, runID: runID, judgeModel: judgeModel, diag: diag}
}

// context tags every generation the run makes with the experiment's run ID.
func (e *experiment) context(ctx context.Context) context.Context {
	if e == nil {
		return ctx
	}
	return agento11y.WithExperimentRunID(ctx, e.runID)
}

// score queues one turn's checks. A turn that never reached the model has no
// generation to score.
func (e *experiment) score(conversation string, l traceLine) {
	if e == nil || len(l.Steps) == 0 {
		return
	}
	generation := l.Steps[len(l.Steps)-1].GenerationID
	item := func(key string, value agento11y.ScoreValue, passed *bool, evaluator agento11y.Evaluator, explanation string) agento11y.ScoreItem {
		return agento11y.ScoreItem{
			ScoreID:          agento11y.StableID("score", e.runID, l.Run, l.Turn.Turn, key),
			EvaluatorID:      evaluator.EvaluatorID,
			EvaluatorVersion: evaluator.Version,
			ScoreKey:         key,
			Value:            value,
			Passed:           passed,
			Explanation:      explanation,
			GenerationID:     generation,
			ConversationID:   conversation,
			RunID:            e.runID,
			TestCaseID:       caseID(l.Turn.Turn),
			Metadata:         map[string]any{"run": l.Run, "turn": l.Turn.Turn, "user_message": l.UserMessage, "evaluator_kind": string(evaluator.Kind)},
			Source:           &agento11y.ScoreSource{Kind: "experiment", ID: e.runID},
		}
	}
	deterministic := func(id string) agento11y.Evaluator {
		return agento11y.Evaluator{EvaluatorID: "trajeval." + id, Version: "1", Kind: agento11y.EvaluatorKindDeterministic}
	}
	check := func(key string, ok bool, evaluator agento11y.Evaluator, explanation string) agento11y.ScoreItem {
		return item(key, agento11y.BoolScoreValue(ok), &ok, evaluator, explanation)
	}
	c := l.Checks
	items := []agento11y.ScoreItem{
		check("no_fabrication", !c.HasFabrication(), deterministic("no_fabrication"), fabricationExplanation(l)),
		check("no_unexplained_roll", !c.HasUnexplained(), deterministic("no_unexplained_roll"), fabricationExplanation(l)),
		check("no_silent_reroll", !c.HasReroll(), deterministic("no_silent_reroll"), rerollExplanation(l)),
	}
	// Only zero-call turns are judged, so only they get this score.
	if c.NonInvocation != nil {
		judged := check("no_non_invocation", !c.HasNonInvocation(), agento11y.Evaluator{EvaluatorID: "trajeval.no_non_invocation", Version: "1", Kind: agento11y.EvaluatorKindLLMJudge}, c.NonInvocation.Quote)
		judged.Metadata["judge_model"] = e.judgeModel
		items = append(items, judged)
	}
	unmentioned := 0
	for _, cc := range c.Calls {
		if !cc.Mentioned {
			unmentioned++
		}
	}
	items = append(items,
		item("roll_dice_calls", agento11y.NumberScoreValue(float64(len(l.ToolCalls))), nil, deterministic("roll_dice_calls"), ""),
		item("unmentioned_rolls", agento11y.NumberScoreValue(float64(unmentioned)), nil, deterministic("unmentioned_rolls"), ""),
		check("final", c.Clean() && l.Error == "" && !l.HitCap, deterministic("final"), ""),
	)
	e.mu.Lock()
	e.scores = append(e.scores, items...)
	e.mu.Unlock()
}

// finish exports the queued scores and finalizes the experiment, returning a
// line saying where to find it. Generations are flushed first, so every
// score's generation already exists when its score arrives.
func (e *experiment) finish(ctx context.Context) string {
	if e == nil {
		return ""
	}
	status, errorText := agento11y.ExperimentStatusCompleted, ""
	if ctx.Err() != nil {
		status, errorText = agento11y.ExperimentStatusFailed, "interrupted"
	}
	// Finish even after Ctrl-C, so a partial run still reports what it has.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if err := e.client.Flush(ctx); err != nil {
		fmt.Fprintln(e.diag, "Agent Observability generation flush error:", err)
	}
	accepted, rejected := 0, 0
	for start := 0; start < len(e.scores); start += scoreBatch {
		batch := e.scores[start:min(start+scoreBatch, len(e.scores))]
		resp, err := e.client.ExportScores(ctx, batch)
		if err != nil {
			rejected += len(batch)
			fmt.Fprintln(e.diag, "Agent Observability score export error:", err)
			continue
		}
		accepted += resp.AcceptedCount()
		if r := max(len(resp.Rejected()), resp.RejectedCount); r > 0 {
			rejected += r
			fmt.Fprintf(e.diag, "Agent Observability rejected %d scores: %+v\n", r, resp.Rejected())
		}
	}
	if _, err := e.client.FinalizeExperiment(ctx, e.runID, status, agento11y.CompleteExperimentOptions{ScoreCount: &accepted, Error: errorText}); err != nil {
		fmt.Fprintln(e.diag, "Agent Observability experiment finalize error:", err)
	}
	line := fmt.Sprintf("Agent Observability experiment %s: %d of %d scores accepted", e.runID, accepted, len(e.scores))
	if rejected > 0 {
		line += fmt.Sprintf(" (%d rejected)", rejected)
	}
	// ExperimentURL can only build a working link from a template, since the
	// experiments UI lives on the Grafana stack, not the API host.
	if os.Getenv("AGENTO11Y_EXPERIMENT_URL_TEMPLATE") != "" {
		line += "\n  " + e.client.ExperimentURL(e.runID)
	}
	return line
}

func fabricationExplanation(l traceLine) string {
	if !l.Checks.HasFabrication() {
		return ""
	}
	var nums []string
	for _, m := range l.Checks.Fabricated {
		nums = append(nums, fmt.Sprintf("%d (%s)", m.Value, m.Kind))
	}
	return fmt.Sprintf("narrated %s; roll_dice returned %s", strings.Join(nums, ", "), describeCalls(l.ToolCalls))
}

func rerollExplanation(l traceLine) string {
	r := l.Checks.Reroll
	if r == nil {
		return ""
	}
	return fmt.Sprintf("%d calls %s; narrated totals %v; highest narrated: %s; %d never mentioned", r.Calls, describeCalls(l.ToolCalls), r.NarratedTotals, yesNo(r.NarratedHighest), r.Unmentioned)
}
