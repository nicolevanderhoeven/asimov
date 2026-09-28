package gm

import (
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func traceEvent(r game.Roll) trace.EventOption {
	return trace.WithAttributes(attribute.String("roll.label", r.Label), attribute.IntSlice("roll.dice", r.Dice), attribute.Int("roll.modifier", r.Modifier), attribute.Int("roll.total", r.Total), attribute.Int("roll.target", r.Target), attribute.Bool("roll.success", r.Success))
}
