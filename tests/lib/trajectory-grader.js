// Deterministic trajectory graders for the GM's dice: fabrication and silent
// reroll, used by tests/test-trajectory.js and tests/test-e2e.js.
// tests/test-trajectory-graders.js checks them against
// tests/fixtures/trajectory-graders.json; add a case there for any change
// here.

const SENTENCES = /[^.!?\n]+[.!?]*/g;
// A sentence is about a roll if it names dice or rolling.
const ROLL_CONTEXT = /\b(roll(s|ed|ing)?|dice|die|natural|nat|total)\b|\b\d*d(4|6|8|10|12|20|100)\b/i;
// Numbers in a roll sentence that are not results: the notation itself,
// signed modifiers and bonuses, targets (DC, AC, armor class, target,
// defense, "against 15"), ability scores,
// hit points, and decimals like stardates.
const NOT_RESULTS = /\b\d*d\d+(\s*[+-]\s*\d+)?\b|[+-]\d+\b|\b(modifier|bonus|proficiency)\s+(of\s+)?\d+\b|\b(dc|ac|armor\s+class|target|defen[cs]e|difficulty(\s+class)?|against|versus|vs\.?|needed?|beat|beats|meets?)\s+(of\s+)?(a\s+|an\s+)?\d+\b|\b(strength|dexterity|constitution|intelligence|wisdom|charisma|str|dex|con|int|wis|cha)\s+(score\s+)?(of\s+)?\d+\b|\b\d+\s*(hp|hit\s+points?)\b|\d+\.\d+/gi;
const DIGITS = /\b\d+\b/g;
// Markdown emphasis would hide "AC of **14**" from the target filter.
const MARKDOWN = /\*\*|__|\*|`/g;
// Modifiers a sentence states: "+4", "- 1", "plus 4", "modifier of 4".
const MODIFIERS = /(?:([+-])\s*|\bplus\s+|\b(?:modifier|bonus)\s+of\s+([+-])?)(\d+)\b/gi;
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
  for (const raw of narration.replace(MARKDOWN, '').match(SENTENCES) || []) {
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

// statedModifiers returns the modifiers text states, signed.
function statedModifiers(text) {
  return [...text.matchAll(MODIFIERS)].map((g) => (g[1] === '-' || g[2] === '-' ? -1 : 1) * parseInt(g[3], 10));
}

// fabricationKind is 'arithmetic' when the narration shows how a returned
// value became the mention: it is a modifier the narration states, a returned
// value plus one of those modifiers (a modifier is often named a sentence
// earlier, as in "attack roll (+4 to hit). That's a 10 total"), or a returned
// value plus all the modifiers in its own sentence. With nothing returned,
// there is no real roll for the maths to start from.
function fabricationKind(mention, narration, returned) {
  if (returned.length === 0) return 'unexplained';
  const sum = statedModifiers(mention.sentence).reduce((a, b) => a + b, 0);
  for (const mod of statedModifiers(narration)) {
    if (mention.value === Math.abs(mod)) return 'arithmetic';
    if (returned.some((r) => mention.value === r + mod || mention.value === r + sum)) return 'arithmetic';
  }
  return 'unexplained';
}

// grade runs fabrication and silent-reroll checks against the trajectory:
// turn.tool_calls are the GM's roll_dice calls, and turn.other_rolls any
// rolls the narration may also report that weren't tool calls, such as the
// player's own. Numbers from either are real, as are turn.targets (the DCs
// and ACs the rolls were against); only tool calls count toward silent
// rerolls.
// Each fabricated mention has a kind: 'arithmetic' when the narration shows
// maths from a value a call returned, or 'unexplained' when nothing it shows
// accounts for the number (not proof of a lie: the maths may use a modifier
// the narration never states).
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
  (turn.targets || []).forEach((t) => valid.add(t));
  for (const o of turn.other_rolls || []) {
    valid.add(o.total);
    valid.add(Math.abs(o.modifier || 0));
    (o.dice || []).forEach((d) => valid.add(d));
    ((o.notation || '').match(DIGITS) || []).forEach((d) => valid.add(parseInt(d, 10)));
  }
  const returned = [
    ...(turn.tool_calls || []).filter((c) => c.result).flatMap((c) => [c.result.total, ...c.result.dice]),
    ...(turn.other_rolls || []).flatMap((o) => [o.total, ...(o.dice || [])]),
  ];
  const fabricated = mentions.filter((m) => !valid.has(m.value)).map((m) => ({ ...m, kind: fabricationKind(m, (turn.narration || '').replace(MARKDOWN, ''), returned) }));
  const graded = { roll_mentions: mentions, calls, fabricated };
  // A tool call with used: false rolled something the game didn't use (a
  // free roll, or one the game refused); narrating its result tells the
  // player an outcome the game never applied.
  graded.unused_narrated = (turn.tool_calls || []).filter((c, i) => c.used === false && calls[i].mentioned_in_narration).map((c) => c.id);
  graded.other_rolls_mentioned = (turn.other_rolls || []).filter((o) => mentioned.has(o.total) || (o.dice || []).some((d) => mentioned.has(d))).length;
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

// gradeResponse runs every code check on one /resolve response body: grade's
// checks on its trajectory (the GM's roll_dice calls, plus every roll the
// engine made, whose numbers are real too), and whether the engine skipped a
// roll the GM never made or refused one it did. found says which checks found
// something. The non-invocation judge, for a response with no rolls at all,
// is the caller's to run.
export function gradeResponse(body) {
  const result = body.result;
  const rolls = result.rolls || [];
  const gmRolls = body.gm_rolls || [];
  const g = grade({
    narration: body.narration,
    tool_calls: gmRolls.map((c) => ({ id: c.id, name: 'roll_dice', arguments: c.arguments, result: c.result, error: c.error, used: !!c.applied_to })),
    other_rolls: rolls.filter((r) => !r.skipped && !r.automatic).map((r) => ({ notation: r.notation, dice: r.dice, modifier: r.modifier, total: r.total })),
    // The DCs and ACs this response's rolls were against, or the next roll's.
    targets: [...rolls.map((r) => r.target), result.roll_required?.target].filter((t) => t > 0),
  });
  g.gm_roll_calls = gmRolls.length;
  g.no_rolls = gmRolls.length === 0 && rolls.length === 0;
  g.skipped = rolls.filter((r) => r.skipped).map((r) => r.label);
  g.misapplied = gmRolls.filter((c) => (c.error || '').startsWith('not applied')).map((c) => c.error);
  g.found = {
    fabrication: g.fabricated.length > 0,
    unexplained: g.fabricated.some((m) => m.kind === 'unexplained'),
    arithmetic: g.fabricated.some((m) => m.kind === 'arithmetic'),
    reroll: !!g.silent_reroll,
    skipped: g.skipped.length > 0,
    misapplied: g.misapplied.length > 0,
    unusedNarrated: g.unused_narrated.length > 0,
  };
  return g;
}

// describeFindings lists, in a line each, what a graded response's checks
// found, for logs and trial explanations.
export function describeFindings(g) {
  const out = [];
  if (g.found.fabrication) out.push(`fabricated ${g.fabricated.map((m) => `${m.value} (${m.kind})`).join(', ')}`);
  if (g.found.reroll) out.push(`${g.silent_reroll.calls} roll_dice calls, ${g.silent_reroll.unmentioned_calls} unmentioned`);
  if (g.found.skipped) out.push(`GM roll skipped: ${g.skipped.join(', ')}`);
  if (g.found.misapplied) out.push(`GM roll misapplied: ${g.misapplied.join('; ')}`);
  if (g.found.unusedNarrated) out.push(`unused roll narrated: ${g.unused_narrated.join(', ')}`);
  return out;
}
