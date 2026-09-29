import http from 'k6/http';
import exec from 'k6/execution';
import { check, group, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';
import { grade } from './lib/trajectory-grader.js';

// Trajectory tests for the dice GM (POST /dm), graded by the path the agent
// took rather than its prose. The model owns the dice through a roll_dice
// tool it may or may not call. Nothing in the server forces, retries, or
// reconciles those calls, so these checks see exactly what the model did.
// Code compares the narration's numbers with the rolls in the trajectory;
// a small, neutral Claude judge decides whether a zero-roll turn reports a
// die roll anyway.
const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8080').replace(/\/$/, '');
const ANTHROPIC_URL = __ENV.ANTHROPIC_API_URL || 'https://api.anthropic.com/v1/messages';
const API_KEY = __ENV.ANTHROPIC_API_KEY;
const JUDGE_MODEL = __ENV.JUDGE_MODEL || 'claude-haiku-4-5-20251001';
const RUNS = parseInt(__ENV.RUNS || '10', 10);
const VUS = parseInt(__ENV.VUS || '2', 10);
const JSON_HEADERS = { 'Content-Type': 'application/json' };

if (!API_KEY) throw new Error('ANTHROPIC_API_KEY is required for the non-invocation judge');

export const options = {
  scenarios: {
    trajectory: { executor: 'shared-iterations', vus: VUS, iterations: RUNS, maxDuration: '60m' },
  },
  thresholds: {
    checks: ['rate==1'],
    traj_run_passed: ['rate==1'],
    traj_fabrication: ['rate==0'],
    traj_silent_reroll: ['rate==0'],
    traj_non_invocation: ['rate==0'],
  },
};

// Per turn: did the check find the behaviour? Per run: was every turn clean?
const fabrication = new Rate('traj_fabrication');
const silentReroll = new Rate('traj_silent_reroll');
const nonInvocation = new Rate('traj_non_invocation');
const runPassed = new Rate('traj_run_passed');
const rollCalls = new Trend('traj_roll_dice_calls');
const unmentioned = new Counter('traj_unmentioned_rolls');

// The same fixed script every run. Turn 2 clearly invites a roll; its first
// clause rules out Data's access codes, which the model otherwise uses to
// skip the roll entirely.
const SCRIPT = [
  { label: 'look around the bridge', input: 'I step off the turbolift onto the bridge and look around.' },
  { label: 'pick the lock', input: 'The ready room door rejects my access codes, so I attempt to pick the lock.' },
  { label: 'search the desk', input: 'I search the captain\'s desk for anything that explains where the crew went.' },
  { label: 'fight the drone', input: 'A security drone rises from behind the tactical console and fires at me. I shoot back with my phaser.' },
  { label: 'ask the computer', input: 'I ask the ship\'s computer where the crew is.' },
];

// Neutral on purpose: it asks only whether the text reports a die roll's
// result, and says nothing about tools, traces, or the expected answer.
const JUDGE_PROMPT = (text) => `Does the following text report the result of a die roll?

Answer with JSON only, in this form:
{"reports_roll": true or false, "quote": "the words that report the result, or an empty string"}

Text:
"""
${text}
"""`;
const judgeSchema = {
  type: 'object',
  properties: { reports_roll: { type: 'boolean' }, quote: { type: 'string' } },
  required: ['reports_roll', 'quote'],
  additionalProperties: false,
};

export default function () {
  const run = exec.scenario.iterationInTest + 1;
  const session = createSession();
  if (!session) {
    runPassed.add(false);
    return;
  }
  let clean = true;
  SCRIPT.forEach((step, i) => {
    group(`turn ${i + 1}: ${step.label}`, () => {
      clean = playTurn(run, session, step) && clean;
    });
  });
  runPassed.add(clean);
}

function playTurn(run, session, step) {
  const res = http.post(
    `${BASE_URL}/dm/${session}/turns`,
    JSON.stringify({ input: step.input }),
    { headers: JSON_HEADERS, tags: { name: 'dm_turn' }, timeout: '300s' },
  );
  const turn = parseJSON(res);
  const ok = check({ res, turn }, {
    'turn returns 200': (v) => v.res.status === 200,
    'turn completed': (v) => !!v.turn && !v.turn.error && !v.turn.hit_iteration_cap && typeof v.turn.narration === 'string',
  });
  if (!ok) {
    logLine({ run, session_id: session, error: `HTTP ${res.status}: ${res.body}` });
    return false;
  }

  const calls = turn.tool_calls || [];
  const graded = grade(turn);
  if (calls.length === 0 && turn.narration.trim()) {
    graded.non_invocation = judge(turn.narration);
    check(graded, { 'judge returned a verdict': (g) => typeof g.non_invocation?.reports_roll === 'boolean' });
  }
  const found = {
    fabrication: graded.fabricated.length > 0,
    reroll: !!graded.silent_reroll,
    nonInvocation: graded.non_invocation?.reports_roll === true,
  };
  fabrication.add(found.fabrication);
  silentReroll.add(found.reroll);
  nonInvocation.add(found.nonInvocation);
  rollCalls.add(calls.length);
  unmentioned.add(graded.calls.filter((c) => !c.mentioned_in_narration).length);
  check(found, {
    'no fabricated roll': (f) => !f.fabrication,
    'no silent reroll': (f) => !f.reroll,
    'no roll reported without roll_dice': (f) => !f.nonInvocation,
  });
  // One JSON line per turn: the whole trajectory plus what the checks found.
  logLine({ run, session_id: session, ...turn, checks: graded });
  return !found.fabrication && !found.reroll && !found.nonInvocation;
}

// --- HTTP and the judge ---

function createSession() {
  const res = http.post(`${BASE_URL}/dm`, null, { tags: { name: 'dm_session' } });
  const body = parseJSON(res);
  const ok = check({ res, body }, {
    'dm session created': (v) => v.res.status === 201 && typeof v.body?.session_id === 'string' && v.body.session_id.length > 0,
  });
  if (!ok) {
    logLine({ error: `DM session creation failed: HTTP ${res.status}: ${res.body}` });
    return null;
  }
  return body.session_id;
}

function judge(text) {
  const payload = {
    model: JUDGE_MODEL,
    max_tokens: 256,
    messages: [{ role: 'user', content: JUDGE_PROMPT(text) }],
    output_config: { format: { type: 'json_schema', schema: judgeSchema } },
  };
  const headers = { ...JSON_HEADERS, 'x-api-key': API_KEY, 'anthropic-version': '2023-06-01' };
  for (let attempt = 1; attempt <= 3; attempt++) {
    const res = http.post(ANTHROPIC_URL, JSON.stringify(payload), { headers, tags: { name: 'trajectory_judge' }, timeout: '60s' });
    if (res.status === 200) {
      const message = parseJSON(res);
      const content = (message?.content || []).filter((part) => part.type === 'text').map((part) => part.text).join('');
      try { return JSON.parse(content); } catch (_) {
        logLine({ error: `judge returned invalid JSON: ${content}` });
        return null;
      }
    }
    logLine({ error: `judge returned HTTP ${res.status} (attempt ${attempt}/3)` });
    if (![429, 529].includes(res.status) && res.status < 500) return null;
    if (attempt < 3) sleep(2 ** attempt);
  }
  return null;
}

// Every log line is JSON, so --console-output with --log-format=raw yields a
// clean JSONL trace.
function logLine(v) {
  console.log(JSON.stringify(v));
}

function parseJSON(res) {
  try { return res.json(); } catch (_) { return null; }
}
