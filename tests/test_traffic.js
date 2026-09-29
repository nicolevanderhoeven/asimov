import http from 'k6/http';
import { sleep, check } from 'k6';
import { randomIntBetween, randomItem } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080'; // The Go app's HTTP API
const anthropicApiKey = __ENV.ANTHROPIC_API_KEY; // Optional: enables LLM-chosen actions instead of a random pick

// Malformed requests and post-ending actions deliberately hit 400/409;
// without this, k6 would count those as http_req_failed even on the
// intentionally-bad-request path this file exists to exercise.
http.setResponseCallback(http.expectedStatuses(200, 201, 400, 409));

// This is a traffic-generation load, not a correctness suite. Per-session
// state means each VU now safely owns its own uninterrupted game — that
// would technically let this file assert exact state transitions too, but
// deliberately keep the checks status/latency-only: that's
// test_functional.js's job, and tracking each VU's expected state across a
// long ramping run would add bookkeeping with no benefit to this file's
// actual purpose (populated metrics/traces/logs in Grafana under load).
export const options = {
  scenarios: {
    traffic: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '1m', target: 5 },   // ramp up
        { duration: '5m', target: 10 },  // sustain
        { duration: '1m', target: 0 },   // ramp down
      ],
      gracefulRampDown: '10s',
    },
  },
  cloud: {
    projectID: 7624575,
    name: 'Asimov traffic generation',
  },
  thresholds: {
    // Loose on purpose: intentionally-bad requests below inflate the error
    // rate a little so error paths also show up in telemetry. Scoped to
    // {name:app} — our own app's requests — so an optional live Anthropic
    // call in chooseAction() (tagged {name:anthropic} below) can't trip a
    // threshold meant to catch regressions in our own app under load.
    'http_req_failed{name:app}': ['rate<0.10'],
    'http_req_duration{name:app}': ['p(95)<5000'],
  },
};

// VU-scoped: k6 re-runs default() per iteration, but a `let` at module
// scope persists across one VU's iterations, so each VU creates its
// session once and reuses it for the rest of its run. currentState tracks
// the latest known state so playTurn always chooses from actions that are
// actually legal right now, instead of a fixed list that drifts out of
// sync with location/combat as the VU plays.
let sessionId = null;
let currentState = null;

export default function() {
  if (!sessionId) {
    const session = createSession();
    if (!session) return;
    sessionId = session.id;
    currentState = session.state;
  }

  // Mostly play a turn, occasionally re-check session status — a rough
  // stand-in for a mix of new and returning traffic.
  if (Math.random() < 0.3) {
    fetchState();
  } else {
    playTurn();
  }
  sleep(randomIntBetween(1, 4));
}

function createSession() {
  const res = http.post(`${BASE_URL}/session`, null, { tags: { name: 'app' } });
  const success = check(res, {
    'session created': (res) => res.status === 201,
  });
  if (!success) {
    console.log(`Session creation failed. Status: ${res.status}, Body: ${res.body}`);
    return null;
  }
  const body = JSON.parse(res.body);
  return { id: body.session_id, state: body.state };
}

// Reads the engine's own available_actions for the current state and asks a
// live model to pick one, rather than a fixed action list — which actions
// are legal changes with location/combat, so a static list drifts out of
// sync and starts submitting actions the engine correctly rejects. Falls
// back to a random available action (still always legal, just not
// LLM-chosen) if no API key is set or the call fails, keeping this file
// runnable — and its traffic varied — without a live key.
function chooseAction(availableActions) {
  if (!anthropicApiKey) {
    return randomItem(availableActions);
  }
  const headers = {
    'Content-Type': 'application/json',
    'x-api-key': anthropicApiKey,
    'anthropic-version': '2023-06-01',
  };
  const optionsText = availableActions
    .map((a, i) => `${i}: kind="${a.kind}" target="${a.target}" — ${a.description}`)
    .join('\n');
  const payload = {
    model: 'claude-sonnet-4-5',
    max_tokens: 10,
    temperature: 0,
    messages: [{
      role: 'user',
      content: `You are choosing the next move for a text-adventure test bot. These are the ONLY currently legal actions:\n${optionsText}\n\nReply with ONLY the number of the action to take, nothing else.`,
    }],
  };
  const res = http.post('https://api.anthropic.com/v1/messages', JSON.stringify(payload), { headers, tags: { name: 'anthropic' } });
  if (res.status !== 200) {
    console.log(`chooseAction: Anthropic call failed (status ${res.status}); picking a random available action`);
    return randomItem(availableActions);
  }
  try {
    const body = JSON.parse(res.body);
    const text = (body.content || []).filter((b) => b.type === 'text').map((b) => b.text).join('').trim();
    const index = parseInt(text.match(/\d+/), 10);
    if (Number.isInteger(index) && availableActions[index]) {
      return availableActions[index];
    }
  } catch (e) {
    // fall through to the random fallback below
  }
  console.log(`chooseAction: could not parse a choice from "${res.body}"; picking a random available action`);
  return randomItem(availableActions);
}

function fetchState() {
  const res = http.get(`${BASE_URL}/session/${sessionId}`, { tags: { name: 'app' } });
  const success = check(res, {
    'status is 200': (res) => res.status === 200,
    'not rate limited': (res) => res.status !== 429,
  });
  if (!success) {
    console.log(`State check failed. Status: ${res.status}, Body: ${res.body}`);
    return;
  }
  currentState = JSON.parse(res.body).state;
}

function playTurn() {
  const headers = { 'Content-Type': 'application/json' };

  // A small slice of intentionally malformed requests, so error-handling
  // paths generate spans/logs too, not only the happy path.
  if (Math.random() < 0.05) {
    const res = http.post(`${BASE_URL}/session/${sessionId}/actions`, '{not valid json', { headers, tags: { name: 'app' } });
    check(res, {
      'bad request handled without 5xx': (res) => res.status < 500,
    });
    return;
  }

  if (!currentState || !currentState.available_actions || currentState.available_actions.length === 0) {
    // The adventure has ended (won or disabled) or state isn't known yet;
    // there's nothing legal left to submit, so just refresh instead.
    fetchState();
    return;
  }

  const chosen = chooseAction(currentState.available_actions);
  const res = http.post(`${BASE_URL}/session/${sessionId}/actions`, JSON.stringify({ kind: chosen.kind, target: chosen.target }), { headers, tags: { name: 'app' } });
  const success = check(res, {
    'status is 200 or 409': (res) => res.status === 200 || res.status === 409, // 409 once the run reaches an end state
    'not rate limited': (res) => res.status !== 429,
  });
  if (!success) {
    console.log(`Play-turn check failed. Status: ${res.status}, Body: ${res.body}`);
  }
  if (res.status === 200) {
    currentState = JSON.parse(res.body).state;
  }
}
