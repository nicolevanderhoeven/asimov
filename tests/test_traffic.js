import http from 'k6/http';
import { sleep, check } from 'k6';
import { randomIntBetween, randomItem } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

const url = 'http://localhost:5050'; // The app URL

// Varied in-character prompts so generated traffic looks organic in Grafana
// dashboards, rather than one message hammered on repeat.
const MESSAGES = [
  'I scan the bridge with my tricorder.',
  'I run a level-two diagnostic on my positronic net.',
  'I head to engineering to check the warp core.',
  'I ask the computer for a status report on the crew.',
  'I examine the ready room for clues.',
  'I attempt to raise the away team on comms.',
  'I review the ship\'s logs for the last twelve hours.',
  'I check life support readings on deck six.',
  'I do an internal scan of my brain to determine its status.',
  'What is the Enterprise?',
];

// This is a traffic-generation load, not a correctness suite: play.py holds
// one global `simulator` for the whole process, so concurrent VUs interleave
// turns into the same conversation transcript. That's fine here — the goal
// is populated metrics/traces/logs in Grafana, not deterministic dialogue —
// but it means checks below stay limited to status/latency. For scripted,
// content-level assertions, use test_functional.js (single VU) instead.
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
    // rate a little so error paths also show up in telemetry.
    http_req_failed: ['rate<0.10'],
    http_req_duration: ['p(95)<5000'],
  },
};

export default function() {
  // Mostly play a turn, occasionally re-fetch the intro — a rough stand-in
  // for a mix of new and returning traffic.
  if (Math.random() < 0.3) {
    fetchIntro();
  } else {
    playTurn();
  }
  sleep(randomIntBetween(1, 4));
}

function fetchIntro() {
  const res = http.get(url);
  const success = check(res, {
    'status is 200': (res) => res.status === 200,
    'not rate limited': (res) => res.status !== 429,
  });
  if (!success) {
    console.log(`Intro check failed. Status: ${res.status}, Body: ${res.body}`);
  }
}

function playTurn() {
  const headers = {
    'Content-Type': 'application/json',
  };

  // A small slice of intentionally malformed requests, so error-handling
  // paths generate spans/logs too, not only the happy path.
  if (Math.random() < 0.05) {
    const res = http.post(url + '/play', '{not valid json', { headers: headers });
    check(res, {
      'bad request handled without 5xx': (res) => res.status < 500,
    });
    return;
  }

  const message = { message: randomItem(MESSAGES) };
  const res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  const success = check(res, {
    'status is 200': (res) => res.status === 200,
    'not rate limited': (res) => res.status !== 429,
  });
  if (!success) {
    console.log(`Play-turn check failed. Status: ${res.status}, Body: ${res.body}`);
  }
}
