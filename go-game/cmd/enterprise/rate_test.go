package main

import (
	"testing"

	"github.com/grafana/agento11y/go/agento11y"
)

func TestParseRating(t *testing.T) {
	for _, tc := range []struct {
		text    string
		value   agento11y.ConversationRatingValue
		comment string
		ok      bool
	}{
		{" good", agento11y.ConversationRatingValueGood, "", true},
		{" Bad the drone fight dragged", agento11y.ConversationRatingValueBad, "the drone fight dragged", true},
		{" + ", agento11y.ConversationRatingValueGood, "", true},
		{"", "", "", false},
		{" great story", "", "", false},
	} {
		value, comment, err := parseRating(tc.text)
		if (err == nil) != tc.ok || value != tc.value || comment != tc.comment {
			t.Errorf("parseRating(%q) = %q, %q, %v", tc.text, value, comment, err)
		}
	}
}
