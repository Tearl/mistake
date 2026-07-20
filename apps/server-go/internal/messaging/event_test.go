package messaging

import "testing"

func TestRecognitionRequestedEvent(t *testing.T) {
	event := NewRecognitionRequested("event-1", "job-1", "dev-user", "https://example.com/uploads/a.jpg")
	if err := event.Validate(); err != nil {
		t.Fatalf("expected valid event: %v", err)
	}
	if event.EventType != RecognitionRequested || event.SchemaVersion != 1 {
		t.Fatalf("unexpected envelope: %#v", event)
	}
}

func TestRecognitionRequestedEventRejectsMissingJob(t *testing.T) {
	event := NewRecognitionRequested("event-1", "", "dev-user", "https://example.com/uploads/a.jpg")
	if err := event.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
