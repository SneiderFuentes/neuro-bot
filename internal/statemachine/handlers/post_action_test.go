package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
)

func testSess(state string) *session.Session {
	return &session.Session{
		ID:           "sess-1",
		PhoneNumber:  "+573001234567",
		CurrentState: state,
		Status:       session.StatusActive,
		Context:      make(map[string]string),
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(2 * time.Hour),
	}
}

func textM(text string) bird.InboundMessage {
	return bird.InboundMessage{
		ID: "msg-1", Phone: "+573001234567",
		MessageType: "text", Text: text, ReceivedAt: time.Now(),
	}
}

func postbackM(payload string) bird.InboundMessage {
	return bird.InboundMessage{
		ID: "msg-pb", Phone: "+573001234567",
		MessageType: "text", IsPostback: true,
		PostbackPayload: payload, Text: payload,
		ReceivedAt: time.Now(),
	}
}

func TestPostActionMenu_OtraCita(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StatePostActionMenu)
	result, err := m.Process(context.Background(), sess, postbackM("otra_cita"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskMedicalOrder {
		t.Errorf("expected ASK_MEDICAL_ORDER, got %s", result.NextState)
	}
	if len(result.ClearCtx) == 0 {
		t.Error("expected booking keys to be cleared")
	}
}

func TestPostActionMenu_VerCitas(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StatePostActionMenu)
	result, err := m.Process(context.Background(), sess, postbackM("ver_citas"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateFetchAppointments {
		t.Errorf("expected FETCH_APPOINTMENTS, got %s", result.NextState)
	}
}

func TestPostActionMenu_CambiarPaciente(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StatePostActionMenu)
	result, err := m.Process(context.Background(), sess, postbackM("cambiar_paciente"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskDocument {
		t.Errorf("expected ASK_DOCUMENT, got %s", result.NextState)
	}
	// Should clear all context
	found := false
	for _, k := range result.ClearCtx {
		if k == "__all__" {
			found = true
		}
	}
	if !found {
		t.Error("expected __all__ in ClearCtx")
	}
}

func TestPostActionMenu_Agente(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StatePostActionMenu)
	result, err := m.Process(context.Background(), sess, textM("agente"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT, got %s", result.NextState)
	}
}

func TestPostActionMenu_AgenteUpperCase(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StatePostActionMenu)
	result, _ := m.Process(context.Background(), sess, textM("AGENTE"))
	if result.NextState != sm.StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT for uppercase, got %s", result.NextState)
	}
}

func TestPostActionMenu_InvalidInput(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StatePostActionMenu)
	result, err := m.Process(context.Background(), sess, textM("hola"))
	if err != nil {
		t.Fatal(err)
	}
	// Should stay in same state with retry
	if result.NextState != sm.StatePostActionMenu {
		t.Errorf("expected POST_ACTION_MENU (retry), got %s", result.NextState)
	}
}

func TestPostActionMenu_Numeric(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StatePostActionMenu)
	// "1" = otra_cita, "2" = ver_citas, "3" = cambiar_paciente
	result, err := m.Process(context.Background(), sess, textM("2"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateFetchAppointments {
		t.Errorf("expected FETCH_APPOINTMENTS for numeric '2', got %s", result.NextState)
	}
}

func TestFarewell(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StateFarewell)
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateTerminated {
		t.Errorf("expected TERMINATED, got %s", result.NextState)
	}
	if len(result.Messages) == 0 {
		t.Error("expected farewell text")
	}
}

func TestTerminated(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StateTerminated)
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if sess.Status != session.StatusCompleted {
		t.Errorf("expected status completed, got %s", sess.Status)
	}
	if result.NextState != sm.StateTerminated {
		t.Errorf("expected TERMINATED, got %s", result.NextState)
	}
}

func TestChangePatient(t *testing.T) {
	m := sm.NewMachine()
	RegisterPostActionHandlers(m)

	sess := testSess(sm.StateChangePatient)
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskDocument {
		t.Errorf("expected ASK_DOCUMENT, got %s", result.NextState)
	}
	found := false
	for _, k := range result.ClearCtx {
		if k == "__all__" {
			found = true
		}
	}
	if !found {
		t.Error("expected __all__ ClearCtx")
	}
}
