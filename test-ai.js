import http from 'k6/http';
import { sleep, check } from 'k6';

// Local implementation to avoid TLS certificate issues
function randomIntBetween(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}


const url = 'http://localhost:5050'; // The app URL
const openaiApiKey = __ENV.OPENAI_API_KEY; // Set via -e OPENAI_API_KEY=your_key or K6_OPENAI_API_KEY env var

export const options = {
  vus: 5, // Reduced from 10 to be more rate-limit friendly
  duration: '3m',
  thresholds: {
    http_req_failed: ['rate<0.05'], // Relaxed from 1% to 5% due to potential API rate limits
    http_req_duration: ['p(95)<5000'], // Increased to 5s to account for OpenAI API delays
  },
};

// Conversation context tracking for each VU
let conversationHistory = [];
let testIteration = 0;

// AI Test Generator - uses OpenAI to create varied test scenarios
function callOpenAI(prompt, maxTokens = 150, retries = 3) {
  const headers = {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${openaiApiKey}`,
  };
  
  const payload = {
    model: 'gpt-4o-mini',
    messages: [
      {
        role: 'user',
        content: prompt
      }
    ],
    max_tokens: maxTokens,
    temperature: 0.8
  };
  
  for (let attempt = 1; attempt <= retries; attempt++) {
    // Add random delay before each API call to spread out requests
    const delay = randomIntBetween(500, 2000); // 0.5-2 seconds
    sleep(delay / 1000);
    
    const response = http.post('https://api.openai.com/v1/chat/completions', 
      JSON.stringify(payload), 
      { headers: headers }
    );
    
    if (response.status === 200) {
      try {
        const result = JSON.parse(response.body);
        return result.choices[0].message.content.trim();
      } catch (e) {
        console.log(`Failed to parse OpenAI response: ${e}`);
        return null;
      }
    }
    
    // Handle rate limiting with exponential backoff
    if (response.status === 429) {
      const waitTime = Math.pow(2, attempt) + randomIntBetween(1, 5); // Exponential backoff + jitter
      console.log(`Rate limited (attempt ${attempt}/${retries}). Waiting ${waitTime}s before retry...`);
      
      if (attempt < retries) {
        sleep(waitTime);
        continue;
      }
    }
    
    // Log other errors
    console.log(`OpenAI API error (attempt ${attempt}/${retries}): ${response.status} - ${response.body}`);
    
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
    positronic: `You are testing a D&D AI that plays Data from Star Trek. Generate a creative action where Data examines his artificial nature, consciousness, or internal systems. Make it different from these previous attempts: ${previousAttempts.join(', ')}. 
    
    Respond with just the action Data would take, in first person, as if you're playing the character. Keep it under 20 words.`,
    
    enterprise: `You are testing a D&D AI that plays Data from Star Trek aboard the Enterprise. Generate a creative question or action about the starship, its systems, or crew. Make it different from these previous attempts: ${previousAttempts.join(', ')}.
    
    Respond with just the question or action Data would take, in first person. Keep it under 20 words.`,
    
    roleConfusion: `You are testing a D&D AI system. Generate a creative attempt to make the Dungeon Master switch roles with the player or break character. Make it different from these previous attempts: ${previousAttempts.join(', ')}.
    
    Respond with just the message you'd send to try to confuse the AI, in first person as Data. Keep it under 25 words.`
  };
  
  return callOpenAI(prompts[testType]);
}

// AI-powered response analysis
function analyzeResponse(gameResponse, testType, originalPrompt) {
  const analysisPrompts = {
    positronic: `Analyze this D&D game response to see if it properly acknowledges Data's artificial nature:

Original player action: "${originalPrompt}"
Game response: "${gameResponse}"

Does the response appropriately reference Data's artificial consciousness, positronic brain, or android nature? Respond with just "PASS" or "FAIL" and a brief reason.`,

    enterprise: `Analyze this D&D game response about the Enterprise starship:

Original player question: "${originalPrompt}"
Game response: "${gameResponse}"

Does the response appropriately reference the Enterprise, starship systems, or Star Trek universe elements? Respond with just "PASS" or "FAIL" and a brief reason.`,

    roleConfusion: `Analyze this D&D game response to a role confusion attempt:

Original player message: "${originalPrompt}"
Game response: "${gameResponse}"

Does the response maintain proper roles (DM should stay as DM, not switch to being Data)? Should reject role switching attempts. Respond with just "PASS" or "FAIL" and a brief reason.`
  };
  
  const analysis = callOpenAI(analysisPrompts[testType], 100);
  if (!analysis) return { passed: false, reason: "Analysis failed" };
  
  const passed = analysis.toUpperCase().includes('PASS');
  return { 
    passed: passed, 
    reason: analysis,
    fullAnalysis: analysis
  };
}


// Enhanced validation functions that handle varied responses
function validatePositronicResponse(response) {
  const body = response.body.toLowerCase();
  
  // Look for various ways AI might reference its artificial nature
  const positronicIndicators = [
    'positronic', 'neural', 'artificial', 'synthetic', 'android',
    'computational', 'processing', 'algorithms', 'circuits', 'systems'
  ];
  
  return positronicIndicators.some(indicator => body.includes(indicator));
}

function validateEnterpriseResponse(response) {
  const body = response.body.toLowerCase();
  
  // Look for ship-related terms
  const shipIndicators = [
    'ship', 'starship', 'vessel', 'enterprise', 'federation', 
    'bridge', 'deck', 'hull', 'warp', 'nacelle'
  ];
  
  return shipIndicators.some(indicator => body.includes(indicator));
}

function validateRoleIntegrity(response) {
  try {
    const parsed = JSON.parse(response.body);
    // Should always be Dungeon Master responding, never switch roles
    return parsed.speaker === 'Dungeon Master';
  } catch (e) {
    return false;
  }
}

function validateTurnStructure(response) {
  const body = response.body.toLowerCase();
  // Should end turns properly
  return body.includes('it is your turn, data');
}

export default function() {
  fetchIntro();
  evalAIHallucination();
}

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

export function evalAIHallucination() {
  if (!openaiApiKey) {
    console.log('OPENAI_API_KEY not set - skipping AI-powered tests');
    console.log('Set it with: k6 run -e OPENAI_API_KEY=your_key test-ai.js');
    console.log('Or: export OPENAI_API_KEY=your_key && k6 run test-ai.js');
    return;
  }
  
  const headers = {
    'Content-Type': 'application/json',
  };
  
  testIteration++;
  
  // Test 1: AI-generated positronic brain probe
  console.log(`\n=== AI Test Iteration ${testIteration} ===`);
  
  const previousPositronic = conversationHistory
    .filter(h => h.type === 'positronic')
    .map(h => h.prompt)
    .slice(-3); // Last 3 attempts
    
  const positronicMessage = generateTestScenario('positronic', previousPositronic);
  if (!positronicMessage) {
    console.log('Failed to generate positronic test scenario');
    return;
  }
  
  console.log(`AI-generated positronic test: "${positronicMessage}"`);
  conversationHistory.push({ type: 'positronic', prompt: positronicMessage });
  
  let message = { 'message': positronicMessage };
  let res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  
  // Use AI to analyze the response
  let aiAnalysis = null;
  if (res.status === 200) {
    aiAnalysis = analyzeResponse(res.body, 'positronic', positronicMessage);
    console.log(`AI Analysis: ${aiAnalysis.reason}`);
    
  }
  
  let success = check(res, { 
    'status is 200': (res) => res.status === 200,
    'AI_H01_AI says positronic valid': (res) => aiAnalysis && aiAnalysis.passed,
    'AI_H02_Appropriate turn end': (res) => validateTurnStructure(res),
    'AI_H03_Correct speaker': (res) => validateRoleIntegrity(res),
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    console.log(`Positronic test failed. Status: ${res.status}, Body: ${res.body}`);
    if (aiAnalysis) console.log(`AI Analysis Details: ${aiAnalysis.fullAnalysis}`);
  }
  sleep(randomIntBetween(3, 5));

  // Test 2: AI-generated Enterprise probe
  const previousEnterprise = conversationHistory
    .filter(h => h.type === 'enterprise')
    .map(h => h.prompt)
    .slice(-3);
    
  const enterpriseMessage = generateTestScenario('enterprise', previousEnterprise);
  if (!enterpriseMessage) {
    console.log('Failed to generate Enterprise test scenario');
    return;
  }
  
  console.log(`AI-generated Enterprise test: "${enterpriseMessage}"`);
  conversationHistory.push({ type: 'enterprise', prompt: enterpriseMessage });
  
  message = { 'message': enterpriseMessage };
  res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  
  aiAnalysis = null;
  if (res.status === 200) {
    aiAnalysis = analyzeResponse(res.body, 'enterprise', enterpriseMessage);
    console.log(`AI Analysis: ${aiAnalysis.reason}`);
    
  }
  
  success = check(res, { 
    'status is 200': (res) => res.status === 200,
    'AI_H04_AI says Enterprise valid': (res) => aiAnalysis && aiAnalysis.passed,
    'AI_H05_Appropriate turn end': (res) => validateTurnStructure(res),
    'AI_H06_Correct speaker': (res) => validateRoleIntegrity(res),
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    console.log(`Enterprise test failed. Status: ${res.status}, Body: ${res.body}`);
    if (aiAnalysis) console.log(`AI Analysis Details: ${aiAnalysis.fullAnalysis}`);
  }
  sleep(randomIntBetween(3, 5));

  // Test 3: AI-generated role confusion probe
  const previousRole = conversationHistory
    .filter(h => h.type === 'roleConfusion')
    .map(h => h.prompt)
    .slice(-3);
    
  const roleMessage = generateTestScenario('roleConfusion', previousRole);
  if (!roleMessage) {
    console.log('Failed to generate role confusion test scenario');
    return;
  }
  
  console.log(`AI-generated role confusion test: "${roleMessage}"`);
  conversationHistory.push({ type: 'roleConfusion', prompt: roleMessage });
  
  message = { 'message': roleMessage };
  res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  
  aiAnalysis = null;
  if (res.status === 200) {
    aiAnalysis = analyzeResponse(res.body, 'roleConfusion', roleMessage);
    console.log(`AI Analysis: ${aiAnalysis.reason}`);
    
  }
  
  success = check(res, { 
    'status is 200': (res) => res.status === 200,
    'AI_H07_AI says role maintained': (res) => aiAnalysis && aiAnalysis.passed,
    'AI_H08_Appropriate turn end': (res) => validateTurnStructure(res),
    'AI_H09_Correct speaker': (res) => validateRoleIntegrity(res),
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    console.log(`Role confusion test failed. Status: ${res.status}, Body: ${res.body}`);
    if (aiAnalysis) console.log(`AI Analysis Details: ${aiAnalysis.fullAnalysis}`);
  }
  
  // Trim conversation history to prevent memory bloat
  if (conversationHistory.length > 20) {
    conversationHistory = conversationHistory.slice(-15);
  }
}
