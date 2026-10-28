import http from 'k6/http';
import exec from 'k6/execution';

// A spending limit for a k6 run, over every model call it causes: the game's
// own (counted by the game server) and the ones these scripts make as player
// and judge. The server keeps the one ledger and prices it (go-game/internal/spend,
// whose prices.go is the only price table); the scripts forward the usage
// Anthropic returns to POST /spend and stop the run once the ledger, minus
// what it held when the run began, reaches the limit.
//
// The limit is K6_MAX_SPEND_USD, in US dollars. It is a guard, not a billing
// control: a call already in flight finishes, and VUs running at once can
// overshoot by a few calls. Set a monthly limit on the Anthropic workspace
// for a hard stop (see tests/README.md).
export const DEFAULT_LIMIT_USD = 5;

// parseLimit reads K6_MAX_SPEND_USD. With explicitRequired (the full
// `make test`, which keeps spending for E2E_DURATION), there is no default.
export function parseLimit(raw, { explicitRequired = false } = {}) {
  if (raw === undefined || raw === null || String(raw).trim() === '') {
    if (explicitRequired) {
      throw new Error(`K6_MAX_SPEND_USD is required for a run with E2E_DURATION, which keeps spending for that long. Set a limit in US dollars, such as K6_MAX_SPEND_USD=60 (a 30-minute run costs about $40-60).`);
    }
    return DEFAULT_LIMIT_USD;
  }
  const n = Number(raw);
  if (!Number.isFinite(n) || n <= 0) {
    throw new Error(`K6_MAX_SPEND_USD must be a number of US dollars above zero, not ${JSON.stringify(raw)}`);
  }
  return n;
}

// sourceOf says who made a Claude call, from the name the scripts tag it
// with: the simulated player (and the probe generator that plays one), or a
// judge. The game master's calls are the server's own, under "gm".
export function sourceOf(name) {
  return /player|generat/.test(name) ? 'player' : 'judge';
}

const key = (e) => `${e.source}\u0000${e.model}`;
const TOKENS = ['input_tokens', 'output_tokens', 'cache_creation_input_tokens', 'cache_read_input_tokens'];

// since is what the ledger spent after the baseline snapshot: the entries of
// now less those of base, in the shape of a snapshot.
export function since(now, base) {
  const before = new Map(((base && base.entries) || []).map((e) => [key(e), e]));
  const entries = [];
  for (const e of now.entries || []) {
    const b = before.get(key(e));
    const d = { ...e, calls: e.calls - (b ? b.calls : 0), usd: e.usd - (b ? b.usd : 0) };
    for (const t of TOKENS) d[t] = (e[t] || 0) - (b ? b[t] || 0 : 0);
    if (d.calls > 0) entries.push(d);
  }
  entries.sort((a, b) => b.usd - a.usd);
  return {
    total_usd: entries.reduce((s, e) => s + e.usd, 0),
    calls: entries.reduce((s, e) => s + e.calls, 0),
    entries,
  };
}

export function overLimit(spent, limit) {
  return spent.total_usd >= limit;
}

const usd = (n) => `$${n.toFixed(2)}`;
const SOURCE_NAMES = { gm: 'game master', player: 'player', judge: 'judge' };

// group totals entries by the string field of each entry that by returns.
function group(entries, by) {
  const sums = new Map();
  for (const e of entries) {
    const k = by(e);
    const s = sums.get(k) || { usd: 0, calls: 0 };
    s.usd += e.usd;
    s.calls += e.calls;
    sums.set(k, s);
  }
  return [...sums].sort((a, b) => b[1].usd - a[1].usd);
}

// summary is the text for the end of a run, or for a run stopped at the
// limit, with the spend by source and by model.
export function summary(spent, limit, { stopped = false } = {}) {
  const lines = [];
  if (stopped) {
    lines.push(`Spend limit reached: ${usd(spent.total_usd)} of ${usd(limit)} (K6_MAX_SPEND_USD). The run was stopped.`);
  } else {
    lines.push(`Estimated spend: ${usd(spent.total_usd)} of the ${usd(limit)} limit (K6_MAX_SPEND_USD), over ${spent.calls} model calls.`);
  }
  if (spent.entries.length > 0) {
    lines.push('By source:');
    for (const [name, s] of group(spent.entries, (e) => e.source)) {
      lines.push(`  ${(SOURCE_NAMES[name] || name).padEnd(12)} ${usd(s.usd).padStart(8)}  ${s.calls} calls`);
    }
    lines.push('By model:');
    for (const [name, s] of group(spent.entries, (e) => e.model)) {
      lines.push(`  ${name.padEnd(28)} ${usd(s.usd).padStart(8)}  ${s.calls} calls`);
    }
    const unpriced = [...new Set(spent.entries.filter((e) => e.priced === false).map((e) => e.model))];
    if (unpriced.length > 0) {
      lines.push(`Priced at the dearest known rate, since prices.go has no entry for: ${unpriced.join(', ')}`);
    }
  }
  lines.push('This is an estimate from the token counts Anthropic returned; the Anthropic Console has the bill.');
  return lines.join('\n');
}

// What a VU knows: where the ledger is, the limit, what it held at the start.
let run = null;

function getSnapshot(baseUrl) {
  const res = http.get(`${baseUrl}/spend`, { tags: { name: 'spend' }, responseCallback: http.expectedStatuses(200) });
  if (res.status !== 200) return null;
  try { return res.json(); } catch (_) { return null; }
}

// startSpend, called from setup(), reads the ledger as it stands and returns
// what to put in setup's data. It fails the run when the server has no
// ledger, since the limit couldn't be kept.
export function startSpend(baseUrl, limit) {
  const base = getSnapshot(baseUrl);
  if (!base) {
    throw new Error(`The game server at ${baseUrl} has no GET /spend, so the spend limit can't be kept. Run this version of the server (make serve) or point BASE_URL at one.`);
  }
  return { baseUrl, limit, base };
}

// useSpend, called at the start of each iteration with setup's data.spend,
// points this VU at the ledger. print says where its messages go (the
// trajectory test prints JSON lines only).
export function useSpend(data, print = console.log) {
  run = { ...data, print };
  checkSpend();
}

function stopIfOver(now) {
  const spent = since(now, run.base);
  if (!overLimit(spent, run.limit)) return;
  // teardown prints the breakdown once the VUs have stopped.
  exec.test.abort(`spend limit of ${usd(run.limit)} reached (${usd(spent.total_usd)} spent)`);
}

// checkSpend stops the run if the ledger, game master included, has reached
// the limit. The scripts call it before each model call and turn.
export function checkSpend() {
  if (!run) return;
  const now = getSnapshot(run.baseUrl);
  if (now) stopIfOver(now);
}

// recordUsage adds a Claude call's usage (the usage of its response) to the
// ledger, then stops the run if that reached the limit. name is the call's
// tag, which says whether the player or a judge made it.
export function recordUsage(name, model, usage) {
  if (!run || !usage) return;
  const res = http.post(`${run.baseUrl}/spend`, JSON.stringify({ source: sourceOf(name), model, usage }), {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: 'spend' },
    responseCallback: http.expectedStatuses(200),
  });
  if (res.status !== 200) {
    run.print(`could not record ${name} spend: HTTP ${res.status}`);
    return;
  }
  let now = null;
  try { now = res.json(); } catch (_) { /* no ledger to read; the next check will */ }
  if (now) stopIfOver(now);
}

// reportSpend, called from teardown(), prints what the run spent.
export function reportSpend(data, print = console.log) {
  if (!data || !data.base) return;
  const now = getSnapshot(data.baseUrl);
  if (!now) {
    print('Could not read the spend from the game server at the end of the run.');
    return;
  }
  const spent = since(now, data.base);
  print(summary(spent, data.limit, { stopped: overLimit(spent, data.limit) }));
}
