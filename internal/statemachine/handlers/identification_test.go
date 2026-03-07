package handlers

import (
	"context"
	"fmt"
	"testing"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/services"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/statemachine/validators"
)

// --- Mock PatientRepository ---

type mockPatientRepo struct {
	findByDocumentFn func(ctx context.Context, doc string) (*domain.Patient, error)
}

func (m *mockPatientRepo) FindByDocument(ctx context.Context, doc string) (*domain.Patient, error) {
	if m.findByDocumentFn != nil {
		return m.findByDocumentFn(ctx, doc)
	}
	return nil, nil
}
func (m *mockPatientRepo) FindByID(ctx context.Context, id string) (*domain.Patient, error) {
	return nil, nil
}
func (m *mockPatientRepo) Create(ctx context.Context, input domain.CreatePatientInput) (string, error) {
	return "", nil
}
func (m *mockPatientRepo) UpdateEntity(ctx context.Context, patientID, entityCode string) error {
	return nil
}

// ==================== AskDocument ====================

func registerAskDocumentConfig(m *sm.Machine) {
	m.RegisterWithConfig(sm.StateAskDocument, sm.HandlerConfig{
		InputType:    sm.InputText,
		TextValidate: validators.Document,
		ErrorMsg:     "Por favor ingresa un número de documento válido (solo números, entre 5 y 15 dígitos).",
		Handler:      askDocumentHandler(),
	})
}

func TestAskDocument_ValidTenDigits(t *testing.T) {
	m := sm.NewMachine()
	registerAskDocumentConfig(m)

	sess := testSess(sm.StateAskDocument)
	result, err := m.Process(context.Background(), sess, textM("1234567890"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StatePatientLookup {
		t.Errorf("expected PATIENT_LOOKUP, got %s", result.NextState)
	}
	if result.UpdateCtx["patient_doc"] != "1234567890" {
		t.Error("expected patient_doc in context")
	}
}

func TestAskDocument_ValidFiveDigits(t *testing.T) {
	m := sm.NewMachine()
	registerAskDocumentConfig(m)

	sess := testSess(sm.StateAskDocument)
	result, _ := m.Process(context.Background(), sess, textM("12345"))
	if result.NextState != sm.StatePatientLookup {
		t.Errorf("expected PATIENT_LOOKUP for 5 digits, got %s", result.NextState)
	}
}

func TestAskDocument_ValidFifteenDigits(t *testing.T) {
	m := sm.NewMachine()
	registerAskDocumentConfig(m)

	sess := testSess(sm.StateAskDocument)
	result, _ := m.Process(context.Background(), sess, textM("123456789012345"))
	if result.NextState != sm.StatePatientLookup {
		t.Errorf("expected PATIENT_LOOKUP for 15 digits, got %s", result.NextState)
	}
}

func TestAskDocument_TooShort(t *testing.T) {
	m := sm.NewMachine()
	registerAskDocumentConfig(m)

	sess := testSess(sm.StateAskDocument)
	result, _ := m.Process(context.Background(), sess, textM("1234"))
	if result.NextState != sm.StateAskDocument {
		t.Errorf("expected ASK_DOCUMENT (retry), got %s", result.NextState)
	}
}

func TestAskDocument_Letters(t *testing.T) {
	m := sm.NewMachine()
	registerAskDocumentConfig(m)

	sess := testSess(sm.StateAskDocument)
	result, _ := m.Process(context.Background(), sess, textM("abc12345"))
	if result.NextState != sm.StateAskDocument {
		t.Errorf("expected ASK_DOCUMENT (retry), got %s", result.NextState)
	}
}

func TestAskDocument_MaxRetries(t *testing.T) {
	m := sm.NewMachine()
	registerAskDocumentConfig(m)

	sess := testSess(sm.StateAskDocument)
	sess.RetryCount = 2 // next invalid will be 3 = maxRetries

	result, _ := m.Process(context.Background(), sess, textM("x"))
	if result.NextState != sm.StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT after max retries, got %s", result.NextState)
	}
}

// ==================== PatientLookup ====================

func TestPatientLookup_Found(t *testing.T) {
	patient := &domain.Patient{
		ID: "PAT001", DocumentNumber: "1234567890",
		FirstName: "Juan", FirstSurname: "Perez",
	}
	repo := &mockPatientRepo{
		findByDocumentFn: func(ctx context.Context, doc string) (*domain.Patient, error) {
			return patient, nil
		},
	}
	svc := services.NewPatientService(repo)

	m := sm.NewMachine()
	RegisterIdentificationHandlers(m, svc)

	sess := testSess(sm.StatePatientLookup)
	sess.Context["patient_doc"] = "1234567890"
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateConfirmIdentity {
		t.Errorf("expected CONFIRM_IDENTITY, got %s", result.NextState)
	}
	if result.UpdateCtx["patient_id"] != "PAT001" {
		t.Error("expected patient_id in context")
	}
}

func TestPatientLookup_NotFound_Agendar(t *testing.T) {
	repo := &mockPatientRepo{} // returns nil
	svc := services.NewPatientService(repo)

	m := sm.NewMachine()
	RegisterIdentificationHandlers(m, svc)

	sess := testSess(sm.StatePatientLookup)
	sess.Context["patient_doc"] = "9999999999"
	sess.Context["menu_option"] = "agendar"
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateRegistrationStart {
		t.Errorf("expected REGISTRATION_START, got %s", result.NextState)
	}
}

func TestPatientLookup_NotFound_Consultar(t *testing.T) {
	repo := &mockPatientRepo{}
	svc := services.NewPatientService(repo)

	m := sm.NewMachine()
	RegisterIdentificationHandlers(m, svc)

	sess := testSess(sm.StatePatientLookup)
	sess.Context["patient_doc"] = "9999999999"
	sess.Context["menu_option"] = "consultar"
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StatePostActionMenu {
		t.Errorf("expected POST_ACTION_MENU, got %s", result.NextState)
	}
}

func TestPatientLookup_DBError(t *testing.T) {
	repo := &mockPatientRepo{
		findByDocumentFn: func(ctx context.Context, doc string) (*domain.Patient, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	svc := services.NewPatientService(repo)

	m := sm.NewMachine()
	RegisterIdentificationHandlers(m, svc)

	sess := testSess(sm.StatePatientLookup)
	sess.Context["patient_doc"] = "1234567890"
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StatePostActionMenu {
		t.Errorf("expected POST_ACTION_MENU on error, got %s", result.NextState)
	}
}

// ==================== ConfirmIdentity ====================

func TestConfirmIdentity_Yes_Consultar(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateConfirmIdentity, confirmIdentityHandler())

	sess := testSess(sm.StateConfirmIdentity)
	sess.Context["menu_option"] = "consultar"
	sess.Context["patient_id"] = "PAT001"

	result, err := m.Process(context.Background(), sess, postbackM("identity_yes"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateFetchAppointments {
		t.Errorf("expected FETCH_APPOINTMENTS, got %s", result.NextState)
	}
}

func TestConfirmIdentity_Yes_Agendar(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateConfirmIdentity, confirmIdentityHandler())

	sess := testSess(sm.StateConfirmIdentity)
	sess.Context["menu_option"] = "agendar"
	sess.Context["patient_id"] = "PAT001"

	result, err := m.Process(context.Background(), sess, postbackM("identity_yes"))
	if err != nil {
		t.Fatal(err)
	}
	// Bird V2: entity already selected before document → skip entity check, go to medical order
	if result.NextState != sm.StateAskMedicalOrder {
		t.Errorf("expected ASK_MEDICAL_ORDER, got %s", result.NextState)
	}
}

func TestConfirmIdentity_No(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateConfirmIdentity, confirmIdentityHandler())

	sess := testSess(sm.StateConfirmIdentity)
	result, err := m.Process(context.Background(), sess, postbackM("identity_no"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskDocument {
		t.Errorf("expected ASK_DOCUMENT, got %s", result.NextState)
	}
	if len(result.ClearCtx) == 0 {
		t.Error("expected patient context keys cleared")
	}
}

func TestConfirmIdentity_InvalidInput(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateConfirmIdentity, confirmIdentityHandler())

	sess := testSess(sm.StateConfirmIdentity)
	result, err := m.Process(context.Background(), sess, textM("maybe"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateConfirmIdentity {
		t.Errorf("expected CONFIRM_IDENTITY (retry), got %s", result.NextState)
	}
}

func TestConfirmIdentity_Yes_DefaultMenu(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateConfirmIdentity, confirmIdentityHandler())

	sess := testSess(sm.StateConfirmIdentity)
	sess.Context["patient_id"] = "PAT001"
	// No menu_option set → defaults to POST_ACTION_MENU

	result, err := m.Process(context.Background(), sess, postbackM("identity_yes"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StatePostActionMenu {
		t.Errorf("expected POST_ACTION_MENU (default), got %s", result.NextState)
	}
}
