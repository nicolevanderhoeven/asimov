// Checks the k6 runner's plumbing without any credentials: the runner built
// and started the game server, passed it the values from .env (read with
// Windows line endings and quotes), and handed the same values to k6. The
// game never calls a model for this.
import http from 'k6/http';
import { check } from 'k6';

export const options = { thresholds: { checks: ['rate==1'] } };

export default function () {
  const res = http.post(`${__ENV.BASE_URL}/session`, '{}', {
    headers: { 'Content-Type': 'application/json' },
  });
  check(res, { 'the game server created a session': (r) => r.status === 200 || r.status === 201 });
  check(__ENV.CI_LIFECYCLE, { 'k6 got the .env value': (v) => v === 'quoted value' });
}
