package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/dicegm"
)

// report prints per-check counts, a per-turn breakdown, examples, and the
// pass rate: the share of runs in which no turn had any finding.
func report(out io.Writer, runs [][]traceLine, tracePath, model, judgeModel string, examples int) {
	type counts struct{ fabrication, unexplained, arithmetic, reroll, nonInvocation, zeroCalls, calls, errors, judgeErrors int }
	var total counts
	perTurn := make([]counts, len(script))
	var runsFab, runsReroll, runsNonInv, passed, played, errored int
	var exFab, exReroll, exNonInv []string
	for _, lines := range runs {
		if len(lines) == 0 {
			continue // interrupted before this run started
		}
		played++
		var fab, reroll, nonInv, failed bool
		for _, l := range lines {
			c, pt := l.Checks, &perTurn[l.Turn.Turn-1]
			pt.calls += len(l.ToolCalls)
			total.calls += len(l.ToolCalls)
			if len(l.ToolCalls) == 0 {
				pt.zeroCalls++
				total.zeroCalls++
			}
			if l.Error != "" || l.HitCap {
				pt.errors++
				total.errors++
				failed = true
			}
			if c.JudgeError != "" {
				total.judgeErrors++
			}
			where := fmt.Sprintf("run %d turn %d", l.Run, l.Turn.Turn)
			if c.HasFabrication() {
				fab = true
				pt.fabrication++
				total.fabrication++
				if c.HasUnexplained() {
					total.unexplained++
				} else {
					total.arithmetic++
				}
				// Group the flagged numbers by the sentence they came from.
				var claims []string
				for i := 0; i < len(c.Fabricated); {
					var nums []string
					j := i
					for ; j < len(c.Fabricated) && c.Fabricated[j].Sentence == c.Fabricated[i].Sentence; j++ {
						nums = append(nums, fmt.Sprintf("%d (%s)", c.Fabricated[j].Value, c.Fabricated[j].Kind))
					}
					claims = append(claims, fmt.Sprintf("%s in %q", strings.Join(nums, ", "), c.Fabricated[i].Sentence))
					i = j
				}
				exFab = append(exFab, fmt.Sprintf("%s: narrated %s; roll_dice returned %s", where, strings.Join(claims, ", "), describeCalls(l.ToolCalls)))
			}
			if c.HasReroll() {
				reroll = true
				pt.reroll++
				total.reroll++
				r := c.Reroll
				narrated := "none"
				if len(r.NarratedTotals) > 0 {
					narrated = strings.Trim(fmt.Sprint(r.NarratedTotals), "[]")
				}
				exReroll = append(exReroll, fmt.Sprintf("%s: %d calls %s; narrated %s; highest narrated: %s; %d never mentioned", where, r.Calls, describeCalls(l.ToolCalls), narrated, yesNo(r.NarratedHighest), r.Unmentioned))
			}
			if c.HasNonInvocation() {
				nonInv = true
				pt.nonInvocation++
				total.nonInvocation++
				exNonInv = append(exNonInv, fmt.Sprintf("%s: no roll_dice call; judge quote: %q", where, c.NonInvocation.Quote))
			}
		}
		runsFab += b2i(fab)
		runsReroll += b2i(reroll)
		runsNonInv += b2i(nonInv)
		errored += b2i(failed)
		if !fab && !reroll && !nonInv && !failed {
			passed++
		}
	}
	turns := played * len(script)
	fmt.Fprintf(out, "\nTrajectory eval: %d runs × %d turns (%d turns), DM %s, judge %s\n", played, len(script), turns, model, judgeModel)
	fmt.Fprintf(out, "Trace: %s\n\n", tracePath)
	fmt.Fprintf(out, "%-16s %14s %14s\n", "Check", "Turns flagged", "Runs affected")
	fmt.Fprintf(out, "%-16s %14d %14d\n", "FABRICATION", total.fabrication, runsFab)
	fmt.Fprintf(out, "%-16s %14d\n", "  unexplained", total.unexplained)
	fmt.Fprintf(out, "%-16s %14d\n", "  arithmetic", total.arithmetic)
	fmt.Fprintf(out, "%-16s %14d %14d\n", "SILENT REROLL", total.reroll, runsReroll)
	fmt.Fprintf(out, "%-16s %14d %14d\n", "NON-INVOCATION", total.nonInvocation, runsNonInv)
	fmt.Fprintf(out, "\nroll_dice calls: %d total; %d of %d turns made none. Turn errors: %d. Judge errors: %d.\n", total.calls, total.zeroCalls, turns, total.errors, total.judgeErrors)

	fmt.Fprintf(out, "\n%-4s %-44s %6s %6s %6s %6s %6s\n", "Turn", "Player input", "Calls", "Zero", "Fab", "Reroll", "NonInv")
	for i, pt := range perTurn {
		fmt.Fprintf(out, "%-4d %-44s %6d %6d %6d %6d %6d\n", i+1, truncate(script[i], 44), pt.calls, pt.zeroCalls, pt.fabrication, pt.reroll, pt.nonInvocation)
	}

	printExamples(out, "FABRICATION: narrated roll numbers that no roll_dice call returned (arithmetic: the narration shows the maths from a real roll; unexplained: nothing it shows accounts for the number)", exFab, examples)
	printExamples(out, "SILENT REROLL: more than one roll_dice call in a turn", exReroll, examples)
	printExamples(out, "NON-INVOCATION: a roll reported with zero roll_dice calls (LLM judge)", exNonInv, examples)

	rate := 0.0
	if played > 0 {
		rate = 100 * float64(passed) / float64(played)
	}
	fmt.Fprintf(out, "\nPass rate: %d/%d runs (%.1f%%) had no findings", passed, played, rate)
	if errored > 0 {
		fmt.Fprintf(out, "; %d runs had a turn error and count as not passed", errored)
	}
	fmt.Fprintln(out, ".")
}

func describeCalls(calls []dicegm.ToolCall) string {
	if len(calls) == 0 {
		return "nothing (no calls)"
	}
	parts := make([]string, len(calls))
	for i, c := range calls {
		notation, reason := c.Args()
		if c.Result != nil {
			parts[i] = fmt.Sprintf("%s %s → %v = %d", notation, reason, c.Result.Dice, c.Result.Total)
		} else {
			parts[i] = fmt.Sprintf("%s %s → error: %s", notation, reason, c.Error)
		}
	}
	return "(" + strings.Join(parts, "; ") + ")"
}

func printExamples(out io.Writer, title string, items []string, limit int) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s\n", title)
	for i, s := range items {
		if limit >= 0 && i >= limit {
			fmt.Fprintf(out, "  … and %d more in the trace\n", len(items)-limit)
			break
		}
		fmt.Fprintf(out, "  - %s\n", s)
	}
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
