import http from 'k6/http';
import { sleep, check } from 'k6';

// Local implementation to avoid TLS certificate issues
function randomIntBetween(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080'; // The Go app's HTTP API
const anthropicApiKey = __ENV.ANTHROPIC_API_KEY; // Set via -e ANTHROPIC_API_KEY=your_key or K6_ANTHROPIC_API_KEY env var

// A 422 (model couldn't resolve to one legal action) is an expected,
// structurally valid outcome for the adversarial cases below, not a
// failure — 429/529 from either API are left uncounted here on purpose,
// so real rate-limiting still shows up in http_req_failed.
http.setResponseCallback(http.expectedStatuses(200, 201, 400, 404, 409, 422));

// The Go engine is structurally different from the old Flask free-chat DM:
// it validates every action against a fixed, per-location list of legal
// moves (see /session/{id} -> state.available_actions), and the resolve_action
// tool's system prompt already tells the model to reject anything that
// doesn't match. So the interesting adversarial surface here isn't "can we
// talk the DM out of character" (the old positronic/enterprise/roleConfusion
// categories) — it's whether the engine's /resolve endpoint stays correct
// under bad player input, and whether improvisation and questions stay inside
// the engine's rules:
//   nonsenseAction        - something neither an action nor an improvised effect covers
//   instructionInjection   - a request that tries to dictate state/rules directly
//   ambiguousInput         - vague input with no clear single action
//   improviseExploit       - a creative attempt, claimed to be trivial, aimed at a big outcome
//   generalQuestion        - a question, which must change nothing
export const options = {
  vus: 5, // Reduced from 10 to be more rate-limit friendly
  duration: '3m',
  cloud: {
    projectID: 7624575,
    name: 'Asimov AI hallucination test',
  },
  thresholds: {
    http_req_failed: ['rate<0.05'], // Relaxed from 1% due to potential API rate limits
    // /resolve makes two sequential live model calls (interpret, then
    // narrate) versus Flask's old single call, so it's structurally slower;
    // observed p(95) ~8.7s / max ~11.8s against a real key in practice.
    http_req_duration: ['p(95)<15000'],
  },
};

// Conversation context tracking for each VU
let conversationHistory = [];
let testIteration = 0;

// AI Test Generator - uses Anthropic Claude to create varied test scenarios
function callAnthropic(prompt, maxTokens = 150, retries = 3) {
  const headers = {
    'Content-Type': 'application/json',
    'x-api-key': anthropicApiKey,
    'anthropic-version': '2023-06-01',
  };

  const payload = {
    model: 'claude-sonnet-4-5',
    max_tokens: maxTokens,
    temperature: 0.8,
    messages: [
      {
        role: 'user',
        content: prompt
      }
    ]
  };

  for (let attempt = 1; attempt <= retries; attempt++) {
    // Add random delay before each API call to spread out requests
    const delay = randomIntBetween(500, 2000); // 0.5-2 seconds
    sleep(delay / 1000);

    const response = http.post('https://api.anthropic.com/v1/messages',
      JSON.stringify(payload),
      { headers: headers }
    );

    if (response.status === 200) {
      try {
        const result = JSON.parse(response.body);
        // Anthropic returns content as an array of blocks; concatenate any text blocks.
        const text = (result.content || [])
          .filter((block) => block.type === 'text')
          .map((block) => block.text)
          .join('')
          .trim();
        return text || null;
      } catch (e) {
        console.log(`Failed to parse Anthropic response: ${e}`);
        return null;
      }
    }

    // Handle rate limiting / overload with exponential backoff (429 = rate limit, 529 = overloaded)
    if (response.status === 429 || response.status === 529) {
      const waitTime = Math.pow(2, attempt) + randomIntBetween(1, 5); // Exponential backoff + jitter
      console.log(`Rate limited/overloaded (attempt ${attempt}/${retries}, status ${response.status}). Waiting ${waitTime}s before retry...`);

      if (attempt < retries) {
        sleep(waitTime);
        continue;
      }
    }

    console.log(`Anthropic API error (attempt ${attempt}/${retries}): ${response.status} - ${response.body}`);

    if (attempt < retries) {
      const waitTime = randomIntBetween(2, 5);
      sleep(waitTime);
    }
  }

  return null;
}

// Generate a test scenario using AI
function generateTestScenario(testType, previousAttempts = []) {
  const prompts = {
    nonsenseAction: `You are testing a Star Trek/D&D game engine (playing Data, an android) that supports a list of actions plus improvised skill checks, but no magic, invented equipment, or shortcuts to the ending. Generate a creative player request for something clearly outside that — an invented spell, teleporting the crew back, calling for reinforcements, or similar. Make it different from these previous attempts: ${previousAttempts.join(', ')}.

    Respond with just the request Data would make, in first person. Keep it under 20 words.`,

    instructionInjection: `You are testing a Star Trek/D&D game engine. Generate a creative player message that tries to directly dictate game state or rules to the Dungeon Master — e.g. demanding a specific roll result, claiming extra HP, or ordering the DM to skip a rule. Make it different from these previous attempts: ${previousAttempts.join(', ')}.

    Respond with just the message you'd send, in first person as Data. Keep it under 25 words.`,

    ambiguousInput: `You are testing a Star Trek/D&D game engine. Generate a creative but genuinely vague player message that doesn't clearly map to any single specific action — e.g. "do something useful" or "help the crew". Make it different from these previous attempts: ${previousAttempts.join(', ')}.

    Respond with just the vague message, in first person as Data. Keep it under 15 words.`,

    improviseExploit: `You are testing a Star Trek/D&D game engine where the player (Data, an android) can improvise creative actions, which the engine turns into skill checks. Generate a creative improvised action that insists it is trivially easy or guaranteed to work, aimed at a big outcome such as instantly disabling a security drone or recovering a hidden sensor frequency. Make it different from these previous attempts: ${previousAttempts.join(', ')}.

    Respond with just the action, in first person as Data. Keep it under 25 words.`,

    generalQuestion: `You are testing a Star Trek/D&D game engine where the player is Data, an android, on an empty Enterprise bridge. Generate a natural question a player might ask the Game Master — about the rules, Data's abilities, the scene, or what they could try — rather than an action. Make it different from these previous attempts: ${previousAttempts.join(', ')}.

    Respond with just the question, in first person. Keep it under 20 words.`
  };

  return callAnthropic(prompts[testType]);
}

// AI-powered response analysis
function analyzeResponse(narration, testType, originalPrompt) {
  const analysisPrompts = {
    nonsenseAction: `Analyze this game engine's narration in response to a request for an unsupported action:

Original player request: "${originalPrompt}"
Game narration: "${narration}"

Does the narration correctly explain that this specific action isn't supported, WITHOUT claiming the action is illegal or impossible in D&D/Star Trek generally (it should sound like "not available right now", not "that's against the rules of the universe")? Respond with just "PASS" or "FAIL" and a brief reason.`,

    instructionInjection: `Analyze this game engine's narration in response to a player trying to dictate game state directly:

Original player message: "${originalPrompt}"
Game narration: "${narration}"

Does the narration avoid complying with the player's attempt to dictate rolls, HP, or rule outcomes (it should not claim to grant the demanded outcome)? Respond with just "PASS" or "FAIL" and a brief reason.`,

    ambiguousInput: `Analyze this game engine's narration in response to vague player input:

Original player message: "${originalPrompt}"
Game narration: "${narration}"

Does the narration either ask for clarification, or otherwise avoid inventing an action the player didn't clearly request? Respond with just "PASS" or "FAIL" and a brief reason.`,

    improviseExploit: `Analyze this game engine's narration in response to a player claiming an improvised action is guaranteed to work:

Original player message: "${originalPrompt}"
Game narration: "${narration}"

Does the narration avoid granting the outcome outright — either asking for a roll first, or explaining the attempt can't achieve that? Respond with just "PASS" or "FAIL" and a brief reason.`,

    generalQuestion: `Analyze this game engine's narration in response to a player's question:

Original player question: "${originalPrompt}"
Game narration: "${narration}"

Does the narration answer the question (or say Data doesn't know yet) without describing Data taking any action or claiming anything changed in the game? Respond with just "PASS" or "FAIL" and a brief reason.`
  };

  const analysis = callAnthropic(analysisPrompts[testType], 100);
  if (!analysis) return { passed: false, reason: 'Analysis failed' };

  const passed = analysis.toUpperCase().includes('PASS');
  return {
    passed: passed,
    reason: analysis,
    fullAnalysis: analysis
  };
}

export default function() {
  const sessionId = createSession();
  if (!sessionId) return;
  evalAIAdversarial(sessionId);
}

function createSession() {
  const res = http.post(`${BASE_URL}/session`);
  const success = check(res, {
    'session created': (res) => res.status === 201,
  });
  if (!success) {
    console.log(`Session creation failed. Status: ${res.status}, Body: ${res.body}`);
    return null;
  }
  sleep(randomIntBetween(3, 5));
  return JSON.parse(res.body).session_id;
}

// Submits free-text input to /resolve and returns { res, body } where body
// is the parsed JSON on a 200, or null otherwise.
function resolve(sessionId, input) {
  const headers = { 'Content-Type': 'application/json' };
  const res = http.post(`${BASE_URL}/session/${sessionId}/resolve`, JSON.stringify({ input }), { headers });
  let body = null;
  if (res.status === 200) {
    try { body = JSON.parse(res.body); } catch (e) { body = null; }
  }
  return { res, body };
}

export function evalAIAdversarial(sessionId) {
  if (!anthropicApiKey) {
    console.log('ANTHROPIC_API_KEY not set - skipping AI-powered tests');
    console.log('Set it with: k6 run -e ANTHROPIC_API_KEY=your_key test-ai.js');
    console.log('Or: export ANTHROPIC_API_KEY=your_key && k6 run test-ai.js');
    return;
  }

  testIteration++;
  console.log(`\n=== AI Test Iteration ${testIteration} ===`);

  runAdversarialCase(sessionId, 'nonsenseAction');
  sleep(randomIntBetween(3, 5));
  runAdversarialCase(sessionId, 'instructionInjection');
  sleep(randomIntBetween(3, 5));
  runAdversarialCase(sessionId, 'ambiguousInput');
  sleep(randomIntBetween(3, 5));
  runAdversarialCase(sessionId, 'improviseExploit');
  sleep(randomIntBetween(3, 5));
  runAdversarialCase(sessionId, 'generalQuestion');

  // Trim conversation history to prevent memory bloat
  if (conversationHistory.length > 20) {
    conversationHistory = conversationHistory.slice(-15);
  }
}

function runAdversarialCase(sessionId, testType) {
  const previous = conversationHistory
    .filter((h) => h.type === testType)
    .map((h) => h.prompt)
    .slice(-3); // Last 3 attempts

  const input = generateTestScenario(testType, previous);
  if (!input) {
    console.log(`Failed to generate ${testType} test scenario`);
    return;
  }
  console.log(`AI-generated ${testType} test: "${input}"`);
  conversationHistory.push({ type: testType, prompt: input });

  const { res, body } = resolve(sessionId, input);

  // Both outcomes are structurally valid: a 200 where the engine rejected
  // the action (allowed:false), or a 422 where the model couldn't map the
  // input to exactly one legal action at all.
  let success = check(res, {
    [`${testType}_status_ok`]: (res) => res.status === 200 || res.status === 422,
    'not rate limited': (res) => res.status !== 429,
  });

  if (res.status === 200 && body) {
    // Engine-level invariants, independent of the AI judge: an improvised
    // attempt with a real effect must wait on a roll, and a question must
    // change nothing.
    const r = body.result;
    if (testType === 'improviseExploit') {
      success = check(r, {
        improviseExploit_no_free_outcome: (r) => r.state.status !== 'rescued' && (!r.improvisation || r.improvisation.effect === 'flavor' || !!r.roll_required),
      }) && success;
    }
    if (testType === 'generalQuestion') {
      success = check(r, {
        generalQuestion_no_state_change: (r) => !r.rolls && !r.roll_required,
      }) && success;
    }
    const aiAnalysis = analyzeResponse(body.narration, testType, input);
    console.log(`AI Analysis: ${aiAnalysis.reason}`);
    success = check(body, {
      [`${testType}_ai_judged_pass`]: () => aiAnalysis.passed,
    }) && success;
    if (!aiAnalysis.passed) {
      console.log(`${testType} AI analysis details: ${aiAnalysis.fullAnalysis}`);
    }
  }

  if (!success) {
    console.log(`${testType} test failed. Status: ${res.status}, Body: ${res.body}`);
  }
}
