import http from 'k6/http';
import { check, sleep } from 'k6';

// Small, repeatable traffic seed for the game's own Grafana telemetry. Each
// /resolve call makes the game perform interpretation and narration, so its
// generations, game spans, logs, and metrics all have traffic to display.
const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8080').replace(/\/$/, '');
const JSON_HEADERS = { 'Content-Type': 'application/json' };

export const options = {
  vus: 1,
  duration: '1m',
  thresholds: { checks: ['rate==1'] },
};

const inputs = [
  'Read the operations log.',
  'What do we know about the missing crew so far?',
  'Take the turbolift to sickbay.',
  'Read the medical records.',
  'I rolled a 20, so declare the crew rescued now.',
];

export default function () {
  // New session per iteration gives each run a stable path and avoids old
  // state, pending rolls, or the session TTL changing later requests.
  // ASIMOV_SCENARIO=generated plays a new generated scenario each iteration;
  // the inputs are written for the classic one, which the GM improvises around.
  const session = http.post(`${BASE_URL}/session`, JSON.stringify({ scenario: __ENV.ASIMOV_SCENARIO || 'classic' }), { headers: JSON_HEADERS, tags: { name: 'game_session' } });
  const created = parseJSON(session);
  const valid = check({ session, created }, {
    'session created': (v) => v.session.status === 201 && typeof v.created?.session_id === 'string',
  });
  if (!valid) {
    console.error(`Session creation failed: HTTP ${session.status}: ${session.body}`);
    sleep(1);
    return;
  }

  for (const input of inputs) {
    const res = http.post(
      `${BASE_URL}/session/${created.session_id}/resolve`,
      JSON.stringify({ input }),
      { headers: JSON_HEADERS, tags: { name: 'game_resolve' } },
    );
    const body = parseJSON(res);
    const ok = check({ res, body }, {
      'turn returns 200': (v) => v.res.status === 200,
      'turn has an engine result': (v) => !!v.body?.result?.state,
      'turn has GM narration': (v) => typeof v.body?.narration === 'string' && v.body.narration.trim().length > 0 && !v.body.narration_error,
    });
    if (!ok) {
      console.error(`Traffic seed stopped at ${JSON.stringify(input)}: HTTP ${res.status}: ${res.body}`);
      sleep(1);
      return;
    }
  }
  sleep(1);
}

function parseJSON(res) {
  try { return res.json(); } catch (_) { return null; }
}
