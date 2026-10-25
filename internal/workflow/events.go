package workflow

import (
	"crypto/rand"
	"encoding/hex"

	"google.golang.org/adk/session"
)

type EventKind string

const (
	EventProgress EventKind = "progress"
	EventResult   EventKind = "result"
)

type IDs struct {
	RequestID string
	ChatID    string
}

type Event struct {
	IDs       IDs
	Kind      EventKind
	Agent     string
	Tool      string
	ToolState string
	Text      string
	Partial   bool
}

func NewRequestID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return "req_" + hex.EncodeToString(id[:]), nil
}

func FromADK(ids IDs, coordinator string, event *session.Event) Event {
	out := Event{IDs: ids, Kind: EventProgress}
	if event == nil {
		return out
	}
	out.Partial = event.Partial
	out.Agent = event.Author
	if event.Author == coordinator && event.IsFinalResponse() {
		out.Kind = EventResult
	}
	if event.Content == nil {
		return out
	}
	for _, part := range event.Content.Parts {
		if part == nil {
			continue
		}
		out.Text += part.Text
		if part.FunctionCall != nil {
			out.Tool = part.FunctionCall.Name
			out.ToolState = "started"
		}
		if part.FunctionResponse != nil {
			out.Tool = part.FunctionResponse.Name
			out.ToolState = "completed"
		}
	}
	return out
}
