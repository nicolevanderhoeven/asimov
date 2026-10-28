import { check } from 'k6';
import { parseLimit, since, overLimit, summary, sourceOf, DEFAULT_LIMIT_USD } from './lib/spend.js';

// Checks the spending limit's logic in tests/lib/spend.js: the limit setting,
// what a run has spent since it began, when it must stop, and the summary it
// prints. No network calls: it needs neither the game nor an API key.
export const options = {
  vus: 1,
  iterations: 1,
  thresholds: { checks: ['rate==1'] },
};

const throws = (fn) => {
  try { fn(); } catch (e) { return String(e.message || e); }
  return '';
};

const entry = (source, model, calls, usd, extra = {}) => ({
  source, model, calls, usd, priced: true,
  input_tokens: calls * 100, output_tokens: calls * 10, cache_creation_input_tokens: 0, cache_read_input_tokens: 0,
  ...extra,
});

export default function () {
  check(null, {
    'the limit defaults to $5': () => parseLimit(undefined) === 5 && DEFAULT_LIMIT_USD === 5 && parseLimit('') === 5,
    'a limit is read as dollars': () => parseLimit('60') === 60 && parseLimit('0.5') === 0.5,
    'a limit that is not a positive number is refused': () => ['0', '-3', 'abc', 'Infinity'].every((v) => throws(() => parseLimit(v)) !== ''),
    'a run that keeps spending needs its own limit': () => throws(() => parseLimit(undefined, { explicitRequired: true })).includes('K6_MAX_SPEND_USD is required'),
    'an explicit limit satisfies it': () => parseLimit('60', { explicitRequired: true }) === 60,
    'players and judges are told apart': () => sourceOf('e2e_player') === 'player' && sourceOf('probe_generator') === 'player' && sourceOf('trajectory_judge') === 'judge' && sourceOf('e2e_ruling_judge') === 'judge',
  });

  // A server that already held spend when the run began.
  const base = { entries: [entry('gm', 'claude-sonnet-4-6', 10, 1.0)] };
  const now = {
    entries: [
      entry('gm', 'claude-sonnet-4-6', 30, 2.5),
      entry('judge', 'claude-opus-5-5', 4, 3.0),
      entry('player', 'claude-sonnet-4-6', 5, 0.25),
    ],
  };
  const spent = since(now, base);
  check(spent, {
    'only the spend since the baseline counts': (s) => Math.abs(s.total_usd - (1.5 + 3.0 + 0.25)) < 1e-9 && s.calls === 29,
    'entries are dearest first': (s) => s.entries.map((e) => e.source).join() === 'judge,gm,player',
    'tokens are counted since the baseline': (s) => s.entries.find((e) => e.source === 'gm').input_tokens === 2000,
    'with no baseline, everything counts': () => Math.abs(since(now, null).total_usd - 5.75) < 1e-9,
    'a run that spent nothing has no entries': () => since(base, base).entries.length === 0 && since(base, base).total_usd === 0,
    'the limit is reached at the limit, not after it': () => overLimit(spent, 4.75) && overLimit(spent, 4) && !overLimit(spent, 4.76),
  });

  const text = summary(spent, 4, { stopped: true });
  check(text, {
    'a stopped run says so, with limit and total': (t) => t.includes('Spend limit reached: $4.75 of $4.00'),
    'it breaks the spend down by source': (t) => t.includes('game master') && t.includes('$1.50') && /judge\s+\$3\.00\s+4 calls/.test(t),
    'it breaks the spend down by model': (t) => /claude-opus-5-5\s+\$3\.00/.test(t) && /claude-sonnet-4-6\s+\$1\.75\s+25 calls/.test(t),
  });
  check(summary(spent, 10), {
    'a run under its limit still reports its spend': (t) => t.includes('Estimated spend: $4.75 of the $10.00 limit') && t.includes('29 model calls'),
  });
  check(summary({ total_usd: 0.1, calls: 1, entries: [entry('gm', 'claude-future-9', 1, 0.1, { priced: false })] }, 5), {
    'models without a price are called out': (t) => t.includes('no entry for: claude-future-9'),
  });
  check(summary({ total_usd: 0, calls: 0, entries: [] }, 5), {
    'a run with no model calls reports $0.00': (t) => t.includes('$0.00') && !t.includes('By source'),
  });
}
