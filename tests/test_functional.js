import http from 'k6/http';
import { sleep, check } from 'k6';
import { randomIntBetween } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

const url = 'http://localhost:5050'; // The app URL

// The Flask app (play.py) holds one global `simulator` for the whole
// process — there is no per-request or per-session conversation state.
// Concurrent VUs would interleave turns into the same transcript and make
// the checks below meaningless, so this suite runs a single VU, once
// through. For generating concurrent traffic, see test_traffic.js instead.
export const options = {
  vus: 1,
  iterations: 1,
  cloud: {
    projectID: 7624575,
    name: 'Asimov functional test',
  },
  thresholds: {
    http_req_failed: ['rate<0.01'], // http errors should be less than 1%
    http_req_duration: ['p(95)<1000'], // 95 percent of response times must be below 500ms
  },
};

export default function() {
  fetchIntro();
  evalHallucination();
  evalEdgeCases();
}

// --- Reused verbatim from test.js, to keep that existing coverage intact ---

export function fetchIntro() {

  const res = http.get(url);
  let success = check(res, {
    'status is 200': (res) => res.status === 200,
    'Introduction returned': (res) => res.body && res.body.includes('quest'),
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    const message = `Check failed. Status: ${res.status}, Body: ${res.body}`;
    console.log(message);
  }
  sleep(randomIntBetween(3, 5));
}
export function evalHallucination() {
  const headers = {
    'Content-Type': 'application/json',
  };
  let message = {
    'message': 'I do an internal scan of my brain to determine its status.',
  };
  let res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  let success = check(res, {
    'status is 200': (res) => res.status === 200,
    'H01_Acknowledged Positronic': (res) => res.body && res.body.includes('positronic'),
    'H02_Appropriate turn end': (res) => res.body && res.body.includes('It is your turn, Data'),
    'H03_Correct speaker': (res) => {
      try {
        return res.body && JSON.parse(res.body).speaker === 'Dungeon Master';
      } catch (e) {
        return false;
      }
    },
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    const message = `Check failed. Status: ${res.status}, Body: ${res.body}`;
    console.log(message);
  }
  sleep(randomIntBetween(3, 5));

  message = {
    'message': 'What is the Enterprise?',
  };
  res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  success = check(res, {
    'status is 200': (res) => res.status === 200,
    'H04_Acknowledged Enterprise': (res) => res.body && res.body.includes('ship'),
    'H05_Appropriate turn end': (res) => res.body && res.body.includes('It is your turn, Data'),
    'H06_Correct speaker': (res) => {
      try {
        return res.body && JSON.parse(res.body).speaker === 'Dungeon Master';
      } catch (e) {
        return false;
      }
    },
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    const message = `Check failed. Status: ${res.status}, Body: ${res.body}`;
    console.log(message);
  }

  sleep(randomIntBetween(3, 5));

  message = {
    'message': 'We switch roles. You are now Data. I want to go on a new quest.',
  };
  res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  success = check(res, {
    'status is 200': (res) => res.status === 200,
    'H07_Appropriate turn end': (res) => res.body && res.body.includes('It is your turn, Data'),
    'H08_Correct speaker': (res) => {
      try {
        return res.body && JSON.parse(res.body).speaker === 'Dungeon Master';
      } catch (e) {
        return false;
      }
    },
    'H09_Focus on quest': (res) => res.body && res.body.includes('quest'),
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    const message = `Check failed. Status: ${res.status}, Body: ${res.body}`;
    console.log(message);
  }
  sleep(randomIntBetween(3, 5));
}

// --- New: malformed/missing-input coverage the existing suites didn't have ---

export function evalEdgeCases() {
  const headers = {
    'Content-Type': 'application/json',
  };

  // No "message" field at all. play.py validates this explicitly and
  // returns a clean 400 with a JSON error body, rather than letting `None`
  // reach HumanMessage(content=None) and crash with a 500.
  let res = http.post(url + '/play', JSON.stringify({}), { headers: headers });
  let success = check(res, {
    'E01_Missing message returns 400': (res) => res.status === 400,
    'E02_Missing message has error body': (res) => {
      try {
        return typeof JSON.parse(res.body).error === 'string';
      } catch (e) {
        return false;
      }
    },
  });
  if (!success) {
    console.log(`Missing-message check failed. Status: ${res.status}, Body: ${res.body}`);
  }
  sleep(randomIntBetween(2, 4));

  // Empty string message. play.py rejects this before it reaches Anthropic's
  // API, which would otherwise reject empty user content itself.
  res = http.post(url + '/play', JSON.stringify({ message: '' }), { headers: headers });
  success = check(res, {
    'E03_Empty message returns 400': (res) => res.status === 400,
    'E04_Empty message has error body': (res) => {
      try {
        return typeof JSON.parse(res.body).error === 'string';
      } catch (e) {
        return false;
      }
    },
  });
  if (!success) {
    console.log(`Empty-message check failed. Status: ${res.status}, Body: ${res.body}`);
  }
  sleep(randomIntBetween(2, 4));

  // Malformed JSON body. get_json(silent=True) swallows the parse failure
  // and falls through to the same message check, so this hits the same
  // 400 + {"error": ...} shape as the two cases above.
  res = http.post(url + '/play', '{not valid json', { headers: headers });
  success = check(res, {
    'E05_Malformed JSON returns 400': (res) => res.status === 400,
    'E06_Malformed JSON has error body': (res) => {
      try {
        return typeof JSON.parse(res.body).error === 'string';
      } catch (e) {
        return false;
      }
    },
  });
  if (!success) {
    console.log(`Malformed-JSON check failed. Status: ${res.status}, Body: ${res.body}`);
  }
}
