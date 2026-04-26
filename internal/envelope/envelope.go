// Package envelope builds the EventEnvelope shape Supportly's ingest endpoint
// expects. The shape mirrors what SDKs and the FastAPI middleware emit, so
// agent-shipped events look identical to SDK events from Supportly's side.
//
// Reference: backend/app/schemas/issue.py:51 in the supportly repo.
package envelope

import (
	"time"

	"github.com/google/uuid"
)

// Envelope is the wire format posted to /api/v1/ingest/events.
type Envelope struct {
	EventID     string                 `json:"event_id"`
	Timestamp   time.Time              `json:"timestamp"`
	ProjectID   string                 `json:"project_id"`
	Platform    string                 `json:"platform"`
	Level       string                 `json:"level"`
	Environment string                 `json:"environment,omitempty"`
	Release     string                 `json:"release,omitempty"`
	ServerName  string                 `json:"server_name,omitempty"`
	Message     string                 `json:"message,omitempty"`
	Exception   *Exception             `json:"exception,omitempty"`
	Tags        map[string]interface{} `json:"tags,omitempty"`
}

// Exception is the parsed error data.
type Exception struct {
	Type       string      `json:"type"`
	Value      string      `json:"value"`
	Stacktrace *Stacktrace `json:"stacktrace,omitempty"`
}

// Stacktrace contains structured frames. Supportly's ingestion validates
// stacktrace.get("frames") so we always send a dict-shape, never a string.
type Stacktrace struct {
	Frames []Frame `json:"frames"`
}

// Frame matches Supportly's normalized frame schema.
type Frame struct {
	Filename    string `json:"filename"`
	Function    string `json:"function"`
	Lineno      int    `json:"lineno,omitempty"`
	ContextLine string `json:"context_line,omitempty"`
}

// New constructs an Envelope with sensible defaults filled in.
// Caller fills in the rest based on what the parser produced.
func New(projectID, platform string) *Envelope {
	return &Envelope{
		EventID:   uuid.NewString(),
		Timestamp: time.Now().UTC(),
		ProjectID: projectID,
		Platform:  platform,
		Level:     "error",
		Tags:      make(map[string]interface{}),
	}
}
