import { check, group } from 'k6';
import { grade, rollMentions } from './lib/trajectory-grader.js';

// Checks the k6 trajectory graders against the shared fixture that
// go-game/internal/trajeval also runs against, so the two copies can't drift
// apart silently. No network calls: it needs neither the game nor an API key.
const fixture = JSON.parse(open('./fixtures/trajectory-graders.json'));

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: {
    checks: ['rate==1'],
  },
};

const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

export default function () {
  group('roll mentions', () => {
    for (const c of fixture.roll_mentions) {
      const got = rollMentions(c.text).map((m) => m.value);
      if (!check(got, { [c.text]: (v) => same(v, c.want) })) {
        console.error(`${JSON.stringify(c.text)}: got ${JSON.stringify(got)}, want ${JSON.stringify(c.want)}`);
      }
    }
  });
  group('turns', () => {
    for (const c of fixture.turns) {
      const g = grade(c.turn);
      const got = {
        fabricated: g.fabricated.map((m) => m.value),
        mentioned: g.calls.map((cc) => cc.mentioned_in_narration),
        silent_reroll: g.silent_reroll
          ? {
            calls: g.silent_reroll.calls,
            narrated_totals: g.silent_reroll.narrated_totals,
            unmentioned_calls: g.silent_reroll.unmentioned_calls,
            narrated_highest: g.silent_reroll.narrated_highest,
          }
          : null,
      };
      const ok = check(got, {
        [`${c.name}: fabricated`]: (v) => same(v.fabricated, c.want.fabricated),
        [`${c.name}: mentioned`]: (v) => same(v.mentioned, c.want.mentioned),
        [`${c.name}: silent reroll`]: (v) => same(v.silent_reroll, c.want.silent_reroll),
      });
      if (!ok) console.error(`${c.name}: got ${JSON.stringify(got)}, want ${JSON.stringify(c.want)}`);
    }
  });
}
