// Package parser turns RawLog values into Envelopes via a layered detector.
//
// Layers (highest confidence first):
//  1. JSON detector — line is JSON with a recognized schema. 99% confidence.
//  2. Framework regex bank — Python/Java/Go/Node/Ruby tracebacks. ~90%.
//  3. Heuristic — frame-shaped lines after an ERROR keyword. ~70%.
//  4. Fallback — treat as a level=error message envelope. Always works.
//
// M1 ships layers 1 and 4. Layers 2 and 3 land in Week 2.
package parser

import (
	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/source"
)

// Parser produces zero or more Envelopes from a RawLog. Returns nil if the
// log doesn't represent an error event the parser cares about (e.g., an
// INFO line that no layer matched).
type Parser interface {
	Parse(raw source.RawLog, projectID string) *envelope.Envelope
	Name() string
}

// Layered runs each parser in order; first non-nil wins. The caller appends
// fallback last so something always matches when an error keyword is seen.
type Layered struct {
	Parsers []Parser
}

// Parse walks the layers and returns the first non-nil envelope.
func (l *Layered) Parse(raw source.RawLog, projectID string) *envelope.Envelope {
	for _, p := range l.Parsers {
		if env := p.Parse(raw, projectID); env != nil {
			// Tag which layer matched — useful for debugging parser quality
			// in dashboards and for the parse_failures_total metric inverse.
			env.Tags["parser"] = p.Name()
			// Merge source tags into the envelope.
			for k, v := range raw.Tags {
				env.Tags[k] = v
			}
			env.Tags["log_source"] = raw.Source
			return env
		}
	}
	return nil
}
