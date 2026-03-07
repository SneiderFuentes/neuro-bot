package tracking

import (
	"context"
	"errors"
	"testing"

	localrepo "github.com/neuro-bot/neuro-bot/internal/repository/local"
	"github.com/neuro-bot/neuro-bot/internal/statemachine"
)

// mockEventStore implements EventStore for testing.
type mockEventStore struct {
	insertedEvents []localrepo.ChatEvent
	batchEvents    []localrepo.ChatEvent
	insertErr      error
	batchErr       error
}

func (m *mockEventStore) Insert(_ context.Context, event *localrepo.ChatEvent) error {
	if m.insertErr != nil {
		return m.insertErr
	}
	m.insertedEvents = append(m.insertedEvents, *event)
	return nil
}

func (m *mockEventStore) InsertBatch(_ context.Context, events []localrepo.ChatEvent) error {
	if m.batchErr != nil {
		return m.batchErr
	}
	m.batchEvents = append(m.batchEvents, events...)
	return nil
}

func TestNewEventTracker(t *testing.T) {
	store := &mockEventStore{}
	tracker := NewEventTracker(store)
	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}
}

func TestLogEvent_Success(t *testing.T) {
	store := &mockEventStore{}
	tracker := NewEventTracker(store)

	tracker.LogEvent(context.Background(), "sess-1", "+573001234567", "test_event", map[string]interface{}{
		"key": "value",
	})

	if len(store.insertedEvents) != 1 {
		t.Fatalf("expected 1 inserted event, got %d", len(store.insertedEvents))
	}
	ev := store.insertedEvents[0]
	if ev.SessionID != "sess-1" {
		t.Errorf("expected session sess-1, got %s", ev.SessionID)
	}
	if ev.PhoneNumber != "+573001234567" {
		t.Errorf("expected phone, got %s", ev.PhoneNumber)
	}
	if ev.EventType != "test_event" {
		t.Errorf("expected test_event, got %s", ev.EventType)
	}
}

func TestLogEvent_Error_NoPropagation(t *testing.T) {
	store := &mockEventStore{insertErr: errors.New("db error")}
	tracker := NewEventTracker(store)

	// Should not panic even on error
	tracker.LogEvent(context.Background(), "sess-1", "+573001234567", "test_event", nil)
}

func TestLogMessageSent(t *testing.T) {
	store := &mockEventStore{}
	tracker := NewEventTracker(store)

	tracker.LogMessageSent(context.Background(), "sess-1", "+573001234567", "text", "bird-msg-123")

	if len(store.insertedEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(store.insertedEvents))
	}
	ev := store.insertedEvents[0]
	if ev.EventType != "message_sent" {
		t.Errorf("expected message_sent, got %s", ev.EventType)
	}
	if ev.EventData["message_type"] != "text" {
		t.Errorf("expected message_type=text, got %v", ev.EventData["message_type"])
	}
	if ev.EventData["bird_message_id"] != "bird-msg-123" {
		t.Errorf("expected bird_message_id, got %v", ev.EventData["bird_message_id"])
	}
}

func TestLogBatch_Empty(t *testing.T) {
	tracker := NewEventTracker(&mockEventStore{})
	// Should be a no-op, no panic
	tracker.LogBatch(context.Background(), "sess-1", "+573001234567", nil)
	tracker.LogBatch(context.Background(), "sess-1", "+573001234567", []statemachine.Event{})
}

func TestLogBatch_MultipleEvents(t *testing.T) {
	store := &mockEventStore{}
	tracker := NewEventTracker(store)

	events := []statemachine.Event{
		{Type: "patient_identified", Data: map[string]interface{}{"doc": "123"}},
		{Type: "menu_selected", Data: map[string]interface{}{"option": "agendar"}},
		{Type: "appointment_created", Data: map[string]interface{}{"id": "APT001"}},
	}

	tracker.LogBatch(context.Background(), "sess-1", "+573001234567", events)

	if len(store.batchEvents) != 3 {
		t.Fatalf("expected 3 batch events, got %d", len(store.batchEvents))
	}
	if store.batchEvents[0].EventType != "patient_identified" {
		t.Errorf("expected patient_identified, got %s", store.batchEvents[0].EventType)
	}
	if store.batchEvents[2].EventType != "appointment_created" {
		t.Errorf("expected appointment_created, got %s", store.batchEvents[2].EventType)
	}
	// All should have same session and phone
	for _, ev := range store.batchEvents {
		if ev.SessionID != "sess-1" {
			t.Errorf("expected session sess-1, got %s", ev.SessionID)
		}
	}
}

func TestLogBatch_Error_NoPropagation(t *testing.T) {
	store := &mockEventStore{batchErr: errors.New("tx error")}
	tracker := NewEventTracker(store)

	events := []statemachine.Event{
		{Type: "test", Data: nil},
	}
	// Should not panic on error
	tracker.LogBatch(context.Background(), "sess-1", "+573001234567", events)
}
