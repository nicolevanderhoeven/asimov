// gmModel is the model the game server runs, for recording in experiments.
// It repeats the choice in go-game/cmd/enterprise/main.go, from the same
// environment the task runner passes to both: ANTHROPIC_MODEL, else claude-sonnet-5-5
// when ASIMOV_GM_FIXES is on, else claude-sonnet-4-6.
export function gmModel() {
  if (__ENV.ANTHROPIC_MODEL) return __ENV.ANTHROPIC_MODEL;
  return ['1', 't', 'true'].includes((__ENV.ASIMOV_GM_FIXES || '').toLowerCase()) ? 'claude-sonnet-5-5' : 'claude-sonnet-4-6';
}
