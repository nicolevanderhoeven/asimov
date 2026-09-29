import http from 'k6/http';
import { sleep, check } from 'k6';
import { randomIntBetween, randomItem } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080'; // The Go app's HTTP API
const anthropicApiKey = __ENV.ANTHROPIC_API_KEY; // Optional: enables LLM-chosen actions instead of a random pick

// The edge cases below deliberately hit 400/404 — without this, k6 counts
// every one of those as an http_req_failed, and the failure-rate threshold
// trips even though the checks are passing.
http.setResponseCallback(http.expectedStatuses(200, 201, 400, 404));

// Single VU, single pass: a deterministic correctness/regression gate.
// Each session is exclusively owned by this one VU/iteration, so there's
// no shared-state caveat to work around anymore — this just exists to keep
// the run cheap and the output easy to read.
export const options = {
  vus: 1,
  iterations: 1,
  cloud: {
    projectID: 7624575,
    name: 'Asimov functional test',
  },
  thresholds: {
    // Scoped to {name:app} — our own app's requests — so an optional live
    // Anthropic call in chooseAction() (tagged {name:anthropic} below) can't
    // trip a threshold meant to catch regressions in our own app's latency.
    'http_req_failed{name:app}': ['rate<0.01'],
    'http_req_duration{name:app}': ['p(95)<1000'],
  },
};

export default function() {
  const session = createSession();
  if (!session) return;
  evalActionSequence(session.id, session.state);
  evalEdgeCases(session.id);
}

function createSession() {
  const res = http.post(`${BASE_URL}/session`, null, { tags: { name: 'app' } });
  const success = check(res, {
    'session created': (res) => res.status === 201,
    'initial turn is 0': (res) => res.body && JSON.parse(res.body).state.turn === 0,
    'initial location is bridge': (res) => res.body && JSON.parse(res.body).state.location === 'bridge',
    'initial HP is max': (res) => res.body && JSON.parse(res.body).state.hp === 24,
  });
  if (!success) {
    console.log(`Session creation failed. Status: ${res.status}, Body: ${res.body}`);
    return null;
  }
  const body = JSON.parse(res.body);
  return { id: body.session_id, state: body.state };
}

// Reads the engine's own available_actions for the current state and asks a
// live model to pick one, rather than hardcoding a fixed sequence — which
// available actions do exist changes with location/combat, so a fixed or
// blindly-random choice can submit an action that isn't actually legal right
// now. Falls back to a random available action (still always legal, just not
// LLM-chosen) if no API key is set or the call fails, so this test can still
// run without a live key.
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

// --- An LLM-chosen action sequence. Assertions are adaptive rather than
// hardcoded to one scripted path, since the specific action taken each turn
// now varies: any action drawn from available_actions is by construction
// currently legal, so it must always come back allowed with the turn
// counter advanced by exactly one; "move" additionally has a deterministic,
// checkable effect on location. ---

function evalActionSequence(sessionId, initialState) {
  const headers = { 'Content-Type': 'application/json' };
  let state = initialState;
  for (let turn = 0; turn < 3; turn++) {
    if (!state.available_actions || state.available_actions.length === 0) {
      console.log('No available actions; ending the sequence early.');
      break;
    }
    const chosen = chooseAction(state.available_actions);
    const beforeTurn = state.turn;
    const res = postAction(sessionId, chosen, headers);
    const success = check(res, {
      [`A${turn}_status_200`]: (res) => res.status === 200,
      [`A${turn}_allowed`]: (res) => res.body && JSON.parse(res.body).allowed === true,
      [`A${turn}_turn_advanced`]: (res) => res.body && JSON.parse(res.body).state.turn === beforeTurn + 1,
    });
    if (chosen.kind === 'move') {
      check(res, {
        [`A${turn}_location_updated`]: (res) => res.body && JSON.parse(res.body).state.location === chosen.target,
      });
    }
    if (!success) {
      console.log(`Action ${chosen.kind}/${chosen.target} check failed. Status: ${res.status}, Body: ${res.body}`);
    }
    if (res.status === 200) {
      state = JSON.parse(res.body).state;
    }
    sleep(randomIntBetween(1, 2));
  }
}

// --- Edge cases: rewritten from Flask's message-validation contract to
// the new action-validation and session-lookup contract. Reaching a 409
// ("adventure has ended") deterministically requires dice rolls, so that
// case is covered by the Go unit tests instead, where a Roller can be
// stubbed directly. ---

function evalEdgeCases(sessionId) {
  const headers = { 'Content-Type': 'application/json' };

  let res = postAction(sessionId, { target: 'logs' }, headers); // missing "kind"
  let success = check(res, {
    'E01_missing kind returns 400': (res) => res.status === 400,
    'E02_missing kind has error body': (res) => {
      try { return typeof JSON.parse(res.body).error === 'string'; } catch (e) { return false; }
    },
  });
  if (!success) console.log(`Missing-kind check failed. Status: ${res.status}, Body: ${res.body}`);
  sleep(randomIntBetween(1, 2));

  res = postAction(sessionId, { kind: 'inspect' }, headers); // missing "target"
  success = check(res, {
    'E03_missing target returns 400': (res) => res.status === 400,
    'E04_missing target has error body': (res) => {
      try { return typeof JSON.parse(res.body).error === 'string'; } catch (e) { return false; }
    },
  });
  if (!success) console.log(`Missing-target check failed. Status: ${res.status}, Body: ${res.body}`);
  sleep(randomIntBetween(1, 2));

  res = http.post(`${BASE_URL}/session/${sessionId}/actions`, '{not valid json', { headers, tags: { name: 'app' } }); // malformed JSON
  success = check(res, {
    'E05_malformed JSON returns 400': (res) => res.status === 400,
    'E06_malformed JSON has error body': (res) => {
      try { return typeof JSON.parse(res.body).error === 'string'; } catch (e) { return false; }
    },
  });
  if (!success) console.log(`Malformed-JSON check failed. Status: ${res.status}, Body: ${res.body}`);
  sleep(randomIntBetween(1, 2));

  res = postAction('does-not-exist', { kind: 'inspect', target: 'logs' }, headers); // unknown session
  success = check(res, {
    'E07_unknown session returns 404': (res) => res.status === 404,
    'E08_unknown session has error body': (res) => {
      try { return typeof JSON.parse(res.body).error === 'string'; } catch (e) { return false; }
    },
  });
  if (!success) console.log(`Unknown-session check failed. Status: ${res.status}, Body: ${res.body}`);
}

function postAction(sessionId, action, headers) {
  return http.post(`${BASE_URL}/session/${sessionId}/actions`, JSON.stringify({ kind: action.kind, target: action.target }), { headers, tags: { name: 'app' } });
}
