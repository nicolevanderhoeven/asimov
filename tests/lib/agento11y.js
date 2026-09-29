import http from 'k6/http';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';

// Minimal Agent Observability experiment client for k6, following the same
// API calls as tests/test-e2e.js. It uses the credentials the game reads to
// export generations; experiments go to that endpoint's API host.
//
// Two API rules shape it:
// - Every experiment and trial write must name the same source: trial writes
//   from any other actor than the experiment's creator are refused.
// - evaluator_kind is rejected as a score field, so each score keeps it in
//   metadata instead.

const JSON_HEADERS = { 'Content-Type': 'application/json' };
const ENDPOINT = __ENV.AGENTO11Y_API_ENDPOINT || __ENV.AGENTO11Y_API_URL || __ENV.AGENTO11Y_ENDPOINT || __ENV.GRAFANA_CLOUD_SIGIL_ENDPOINT || '';
const TENANT = __ENV.GRAFANA_CLOUD_INSTANCE_ID || __ENV.GRAFANA_CLOUD_INSTANCE || '';
const TOKEN = __ENV.GRAFANA_CLOUD_API_KEY || '';
const API = (ENDPOINT.match(/^https?:\/\/[^/]+/) || [''])[0];

// configured reports whether Agent Observability credentials are present.
export const configured = !!(API && TENANT && TOKEN);

// stableID matches the Agent Observability SDK's StableID.
export function stableID(prefix, ...parts) {
  return `${prefix}-${crypto.sha1(parts.map(String).join('\x1f'), 'hex').slice(0, 16)}`;
}

function call(method, path, body, name) {
  return http.request(method, `${API}${path}`, JSON.stringify(body), {
    headers: {
      ...JSON_HEADERS,
      Authorization: `Basic ${encoding.b64encode(`${TENANT}:${TOKEN}`)}`,
      'X-Scope-OrgID': TENANT,
    },
    tags: { name },
    responseCallback: http.expectedStatuses(200, 202),
  });
}

// startExperiment creates an experiment and returns its ID, or null.
export function startExperiment(source, { name, description, tags, metadata }) {
  const id = stableID('exp', source.id, Date.now(), Math.random());
  const res = call('POST', '/api/v1/experiment-runs:upsert', { experiment_id: id, name, description, source, tags, metadata }, 'agento11y_experiment');
  if (res.status !== 200) {
    console.error(`Agent Observability experiment not started; trials will not be recorded: HTTP ${res.status}: ${res.body}`);
    return null;
  }
  console.log(`Agent Observability experiment ${id} started`);
  return id;
}

// finishExperiment completes the experiment once every trial has finished.
export function finishExperiment(source, id) {
  const res = call('POST', `/api/v1/experiment-runs/${encodeURIComponent(id)}:finalize`, { status: 'completed', source }, 'agento11y_experiment');
  if (res.status !== 200) {
    console.error(`Agent Observability experiment ${id} not finalized: HTTP ${res.status}: ${res.body}`);
    return;
  }
  // The experiments UI is on the Grafana stack, not the API host.
  const template = __ENV.AGENTO11Y_EXPERIMENT_URL_TEMPLATE;
  const link = template ? `: ${template.replace('{run_id}', id).replace('{base}', API)}` : '';
  console.log(`Agent Observability experiment ${id} completed${link}`);
}

// recordTrial writes one completed trial with its scores in three calls:
// start the trial, export its scores, complete it. Each score is
// { key, value: {bool}|{number}|{string}, kind, passed?, explanation?,
// metadata? }. It reports whether every call succeeded.
export function recordTrial(source, experimentID, { caseID, attempt, conversationID, generationID, metadata, durationMs, scores }) {
  const trialID = stableID('trial', experimentID, caseID, attempt);
  const started = call('POST', `/api/v1/experiment-runs/${encodeURIComponent(experimentID)}/trials`, {
    trial_id: trialID, test_case_id: caseID, attempt, status: 'running', conversation_id: conversationID, source, metadata,
  }, 'agento11y_trial');
  if (started.status !== 200) {
    console.error(`trial ${caseID} #${attempt} not started: HTTP ${started.status}: ${started.body}`);
    return false;
  }
  const items = scores.map((s) => ({
    score_id: stableID('score', experimentID, trialID, s.key),
    evaluator_id: `${source.id}.${s.key}`,
    evaluator_version: '1',
    score_key: s.key,
    value: s.value,
    ...(s.passed === undefined ? {} : { passed: s.passed }),
    ...(s.explanation ? { explanation: s.explanation.slice(0, 2000) } : {}),
    trial_id: trialID,
    experiment_id: experimentID,
    test_case_id: caseID,
    conversation_id: conversationID,
    ...(generationID ? { generation_id: generationID } : {}),
    metadata: { evaluator_kind: s.kind, task_id: caseID, trial_id: trialID, attempt, ...(s.metadata || {}) },
    source: { kind: 'experiment', id: experimentID },
  }));
  const exported = call('POST', '/api/v1/scores:export', { scores: items }, 'agento11y_scores');
  let results = [];
  try { results = exported.json().results || []; } catch (_) { /* reported below */ }
  const rejected = results.filter((r) => !r.accepted && r.status !== 'duplicate');
  if (exported.status >= 300 || rejected.length) console.error(`trial ${caseID} #${attempt} scores: HTTP ${exported.status}; ${rejected.length} rejected: ${JSON.stringify(rejected).slice(0, 500)}`);
  const completed = call('PATCH', `/api/v1/experiment-runs/${encodeURIComponent(experimentID)}/trials/${encodeURIComponent(trialID)}`, {
    status: 'completed', conversation_id: conversationID, duration_ms: durationMs, source,
  }, 'agento11y_trial');
  if (completed.status !== 200) console.error(`trial ${caseID} #${attempt} not completed: HTTP ${completed.status}: ${completed.body}`);
  return exported.status < 300 && rejected.length === 0 && completed.status === 200;
}
