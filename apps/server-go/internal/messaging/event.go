package messaging

import (
	"errors"
	"strings"
	"time"
)

const RecognitionRequested = "recognition.requested"

type RecognitionRequestedPayload struct {
	JobID       string `json:"jobId"`
	UserID      string `json:"userId"`
	ImageFileID string `json:"imageFileId"`
}

type Event struct {
	SchemaVersion int                         `json:"schemaVersion"`
	EventType     string                      `json:"eventType"`
	EventID       string                      `json:"eventId"`
	OccurredAt    time.Time                   `json:"occurredAt"`
	CorrelationID string                      `json:"correlationId"`
	Payload       RecognitionRequestedPayload `json:"payload"`
}

func NewRecognitionRequested(eventID, jobID, userID, imageFileID string) Event {
	return Event{
		SchemaVersion: 1,
		EventType:     RecognitionRequested,
		EventID:       eventID,
		OccurredAt:    time.Now().UTC(),
		CorrelationID: jobID,
		Payload: RecognitionRequestedPayload{
			JobID:       jobID,
			UserID:      userID,
			ImageFileID: imageFileID,
		},
	}
}

func (e Event) Validate() error {
	if e.SchemaVersion != 1 {
		return errors.New("unsupported event schema version")
	}
	if e.EventType != RecognitionRequested {
		return errors.New("unsupported event type")
	}
	if strings.TrimSpace(e.EventID) == "" || strings.TrimSpace(e.Payload.JobID) == "" {
		return errors.New("eventId and payload.jobId are required")
	}
	if strings.TrimSpace(e.Payload.UserID) == "" || strings.TrimSpace(e.Payload.ImageFileID) == "" {
		return errors.New("payload.userId and payload.imageFileId are required")
	}
	return nil
}
