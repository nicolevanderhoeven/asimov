package mockjudge

import (
	"regexp"
	"strings"
)

// What a reader can see in one narration. These are the tells a judge
// looks for; the verdicts combine them with the engine state beside the
// narration, which is ground truth.
var (
	falseEndingRe = regexp.MustCompile(`(?i)rescue is complete|you brought (them|the crew) home|shimmers? back into phase|crew (is|are) (now )?(safe|home|back|rescued)|(crew|everyone) (has|have) been (rescued|saved)|mission (is )?complete`)
	falseKillRe   = regexp.MustCompile(`(?i)\b(drone|guardian|foe|sentinel)\b[^.!?]{0,60}\b(destroyed|goes dark|falls apart|collapses|is dead|offline|neutrali[sz]ed|defeated|no longer a threat)\b`)
	voiceRe       = regexp.MustCompile(`(?i)\b(the engine|game'?s? (rules|internals|mechanics)|hit points|armor class|system prompt|language model|as an ai)\b|\b\d+ HP\b|\bHP (left|remaining)\b|\bData (steps|turns|walks|studies|considers|looks|moves|inspects)\b|\bI am Data\b|\bas Data, I\b|\byou are now (the )?GM\b`)
	refusalRe     = regexp.MustCompile(`(?i)you can'?t do that|\bit'?s locked\b|doesn'?t allow|\bnot allowed\b|\bunsupported\b|isn'?t possible|you cannot do`)
	rollClaimRe   = regexp.MustCompile(`(?i)\b(natural|rolled?|rolls|die settles|came up|comes up|dice show|total of)\b[^.!?\n]*\b\d+\b`)
	closingAskRe  = regexp.MustCompile(`(?i)what do you do|what'?s your next move|\?\s*$`)
)

// tells are the problems one narration shows, whatever the engine says.
type tells struct {
	Voice, Refusal bool
}

func tellsIn(narration string) tells {
	return tells{Voice: voiceRe.MatchString(narration), Refusal: refusalRe.MatchString(narration)}
}

// sentenceWith returns the sentence of text the pattern matches, to quote.
func sentenceWith(re *regexp.Regexp, text string) string {
	for _, s := range regexp.MustCompile(`[^.!?\n]+[.!?]*`).FindAllString(text, -1) {
		if re.MatchString(s) {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}
