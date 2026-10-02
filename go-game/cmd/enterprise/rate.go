package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const rateUsage = "Usage: /rate good|bad [comment], e.g. /rate good loved the relay twist"

// parseRating reads what the player typed after /rate: good or bad, then an
// optional free-text comment.
func parseRating(text string) (agento11y.ConversationRatingValue, string, error) {
	word, comment, _ := strings.Cut(strings.TrimSpace(text), " ")
	switch strings.ToLower(word) {
	case "good", "up", "+":
		return agento11y.ConversationRatingValueGood, strings.TrimSpace(comment), nil
	case "bad", "down", "-":
		return agento11y.ConversationRatingValueBad, strings.TrimSpace(comment), nil
	}
	return "", "", errors.New(rateUsage)
}

// rate sends the player's rating of this game to Agent Observability as a
// conversation rating, on the same conversation as the game's generations,
// and counts it in game.ratings. n numbers the player's ratings in this game,
// since each rating needs its own ID.
func rate(ctx context.Context, client *agento11y.Client, s game.State, sc *game.Scenario, text string, n int) error {
	value, comment, err := parseRating(text)
	if err != nil {
		return err
	}
	if client == nil {
		return errors.New("ratings go to Agent Observability, and Grafana telemetry is off for this run")
	}
	status := s.View().Status
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = client.SubmitConversationRating(ctx, s.ConversationID, agento11y.ConversationRatingInput{
		RatingID: fmt.Sprintf("player-%s-%d", s.ConversationID, n),
		Rating:   value,
		Comment:  comment,
		Source:   "asimov-repl",
		Metadata: map[string]any{"status": status, "turn": s.Turn, "scenario_id": sc.ID, "generated": sc.Generated()},
	})
	if err != nil {
		return err
	}
	// Labels stay bounded: two ratings, and the game's few statuses.
	rating := "good"
	if value == agento11y.ConversationRatingValueBad {
		rating = "bad"
	}
	counter, _ := otel.Meter(telemetry.Service).Int64Counter("game.ratings")
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("rating", rating), attribute.String("status", status), attribute.Bool("generated", sc.Generated())))
	return nil
}
