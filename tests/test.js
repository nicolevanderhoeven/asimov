import http from 'k6/http';
import { sleep, check } from 'k6';
import { randomIntBetween, randomItem } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080'; // The Go app's HTTP API
const anthropicApiKey = __ENV.ANTHROPIC_API_KEY; // Optional: enables LLM-chosen actions instead of a random pick

http.setResponseCallback(http.expectedStatuses(200, 201));

export const options = {
  vus: 10,
  duration: '3m',
  cloud: {
    projectID: 7624575,
    name: 'Asimov local load test',
  },
  thresholds: {
    // Scoped to {name:app} — our own app's requests — so an optional live
    // Anthropic call in chooseAction() (tagged {name:anthropic} below) can't
    // trip a threshold meant to catch regressions in our own app's latency.
    'http_req_failed{name:app}': ['rate<0.01'], // http errors should be less than 1%
    'http_req_duration{name:app}': ['p(95)<1000'], // 95 percent of response times must be below 1000ms
  },
};

export default function() {
  const session = createSession();
  if (!session) return;
  playActionSequence(session.id, session.state);
}

// Each VU/iteration gets its own session, so concurrent VUs never share
// state — the API's per-session design means this is now safe.
function createSession() {
  const res = http.post(`${BASE_URL}/session`, null, { tags: { name: 'app' } });
  const success = check(res, {
    'session created': (res) => res.status === 201,
  });
  if (!success) {
    console.log(`Session creation failed. Status: ${res.status}, Body: ${res.body}`);
    return null;
  }
  sleep(randomIntBetween(3, 5));
  const body = JSON.parse(res.body);
  return { id: body.session_id, state: body.state };
}

// Reads the engine's own available_actions for the current state and asks a
// live model to pick one, rather than hardcoding a fixed sequence — which
// available actions do exists changes with location/combat, so a fixed or
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

// A short sequence of LLM-chosen actions, each asserted against the fields
// the engine always sets for a legal action (allowed, turn count, and, for
// "move", the resulting location) rather than a hardcoded outcome, since the
// specific action taken now varies turn to turn.
function playActionSequence(sessionId, initialState) {
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
      'status is 200': (res) => res.status === 200,
      [`turn${turn}_allowed`]: (res) => res.body && JSON.parse(res.body).allowed === true,
      [`turn${turn}_turn_advanced`]: (res) => res.body && JSON.parse(res.body).state.turn === beforeTurn + 1,
      [`turn${turn}_message_present`]: (res) => res.body && JSON.parse(res.body).message.length > 0,
      'not rate limited': (res) => res.status !== 429,
    });
    if (chosen.kind === 'move') {
      check(res, {
        [`turn${turn}_location_updated`]: (res) => res.body && JSON.parse(res.body).state.location === chosen.target,
      });
    }
    if (!success) {
      console.log(`Action ${chosen.kind}/${chosen.target} check failed. Status: ${res.status}, Body: ${res.body}`);
    }
    if (res.status === 200) {
      state = JSON.parse(res.body).state;
    }
    sleep(randomIntBetween(3, 5));
  }
}

function postAction(sessionId, action, headers) {
  return http.post(`${BASE_URL}/session/${sessionId}/actions`, JSON.stringify({ kind: action.kind, target: action.target }), { headers, tags: { name: 'app' } });
}
