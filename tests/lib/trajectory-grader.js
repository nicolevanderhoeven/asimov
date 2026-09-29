// Deterministic trajectory graders for the dice GM: fabrication and silent
// reroll. This is a JavaScript copy of go-game/internal/trajeval. Both run
// against tests/fixtures/trajectory-graders.json (k6 run
// tests/test-trajectory-graders.js, and go test ./internal/trajeval), so a
// change to one that the other doesn't match fails a test.

const SENTENCES = /[^.!?\n]+[.!?]*/g;
// A sentence is about a roll if it names dice or rolling.
const ROLL_CONTEXT = /\b(roll(s|ed|ing)?|dice|die|natural|nat|total)\b|\b\d*d(4|6|8|10|12|20|100)\b/i;
// Numbers in a roll sentence that are not results: the notation itself,
// signed modifiers and bonuses, targets (DC/AC/"against 15"), ability scores,
// hit points, and decimals like stardates.
const NOT_RESULTS = /\b\d*d\d+(\s*[+-]\s*\d+)?\b|[+-]\d+\b|\b(modifier|bonus|proficiency)\s+(of\s+)?\d+\b|\b(dc|ac|difficulty(\s+class)?|against|versus|vs\.?|needed?|beat|beats|meets?)\s+(of\s+)?(a\s+|an\s+)?\d+\b|\b(strength|dexterity|constitution|intelligence|wisdom|charisma|str|dex|con|int|wis|cha)\s+(score\s+)?(of\s+)?\d+\b|\b\d+\s*(hp|hit\s+points?)\b|\d+\.\d+/gi;
const DIGITS = /\b\d+\b/g;
const WORDS = /\b[a-z]+(?:-[a-z]+)?\b/gi;
const UNITS = { one: 1, two: 2, three: 3, four: 4, five: 5, six: 6, seven: 7, eight: 8, nine: 9, ten: 10, eleven: 11, twelve: 12, thirteen: 13, fourteen: 14, fifteen: 15, sixteen: 16, seventeen: 17, eighteen: 18, nineteen: 19 };
const TENS = { twenty: 20, thirty: 30, forty: 40, fifty: 50 };
// Spelled-out numbers count only straight after one of these, so "one of the
// dice" is not a roll of 1 but "a seventeen" and "comes up two" are.
const NUMBER_WORD_LEADS = new Set(['a', 'an', 'rolled', 'rolls', 'natural', 'nat', 'of', 'is', 'was', 'showing', 'shows', 'up', 'on']);

function wordValue(word) {
  const w = word.toLowerCase();
  if (w in UNITS) return UNITS[w];
  const [t, u] = w.split('-');
  if (!(t in TENS)) return null;
  if (u === undefined) return TENS[t];
  return u in UNITS && UNITS[u] < 10 ? TENS[t] + UNITS[u] : null;
}

// rollMentions returns the numbers the narration presents as roll results.
export function rollMentions(narration) {
  const out = [];
  for (const raw of narration.match(SENTENCES) || []) {
    const sentence = raw.trim();
    if (!ROLL_CONTEXT.test(sentence)) continue;
    const clean = sentence.replace(NOT_RESULTS, ' ');
    for (const d of clean.match(DIGITS) || []) out.push({ value: parseInt(d, 10), sentence });
    const words = clean.match(WORDS) || [];
    for (let i = 1; i < words.length; i++) {
      const n = wordValue(words[i]);
      if (n !== null && NUMBER_WORD_LEADS.has(words[i - 1].toLowerCase())) out.push({ value: n, sentence });
    }
  }
  return out;
}

// grade runs fabrication and silent-reroll checks against the trajectory.
export function grade(turn) {
  const mentions = rollMentions(turn.narration || '');
  const mentioned = new Set(mentions.map((m) => m.value));
  // Any number a call returned is a legitimate thing to narrate: a die, the
  // modifier, the total, or the notation's count and sides.
  const valid = new Set();
  const totals = [];
  const calls = (turn.tool_calls || []).map((c) => {
    const args = typeof c.arguments === 'object' && c.arguments ? c.arguments : {};
    const cc = { id: c.id, notation: args.notation, reason: args.reason, mentioned_in_narration: false };
    if (c.result) {
      const { dice, modifier, total, notation } = c.result;
      cc.dice = dice;
      cc.total = total;
      totals.push(total);
      valid.add(total);
      valid.add(Math.abs(modifier));
      cc.mentioned_in_narration = mentioned.has(total) || dice.some((d) => mentioned.has(d));
      dice.forEach((d) => valid.add(d));
      (notation.match(DIGITS) || []).forEach((d) => valid.add(parseInt(d, 10)));
    }
    return cc;
  });
  const graded = { roll_mentions: mentions, calls, fabricated: mentions.filter((m) => !valid.has(m.value)) };
  if (calls.length > 1) {
    const highest = Math.max(...totals);
    const narrated = calls.filter((c) => c.mentioned_in_narration && c.total !== undefined).map((c) => c.total);
    graded.silent_reroll = {
      calls: calls.length,
      totals,
      narrated_totals: narrated,
      unmentioned_calls: calls.filter((c) => !c.mentioned_in_narration).length,
      narrated_highest: narrated.includes(highest),
    };
  }
  return graded;
}
