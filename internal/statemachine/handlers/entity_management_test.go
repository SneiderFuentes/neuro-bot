package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/testutil"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
)

// --- ASK_CLIENT_TYPE ---

func TestAskClientType_ValidSelection_EPS(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindActiveByCategoryFn: func(ctx context.Context, category string) ([]domain.Entity, error) {
			return []domain.Entity{
				{Code: "EPS001", Name: "NUEVA EPS", Category: "EPS", IsActive: true},
			}, nil
		},
	}
	m := sm.NewMachine()
	RegisterEntityManagementHandlers(m, entityRepo, &testutil.MockPatientRepo{})

	sess := testSess(sm.StateAskClientType)
	result, err := m.Process(context.Background(), sess, postbackM("ct_2"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskEntityNumber {
		t.Errorf("expected ASK_ENTITY_NUMBER (chained from SHOW_ENTITY_LIST), got %s", result.NextState)
	}
	if sess.GetContext("entity_category") != "EPS" {
		t.Errorf("expected entity_category=EPS, got %s", sess.GetContext("entity_category"))
	}
	if sess.GetContext("client_type") != "EPS" {
		t.Errorf("expected client_type=EPS, got %s", sess.GetContext("client_type"))
	}
}

func TestAskClientType_InvalidText(t *testing.T) {
	m := sm.NewMachine()
	RegisterEntityManagementHandlers(m, &testutil.MockEntityRepo{}, &testutil.MockPatientRepo{})

	sess := testSess(sm.StateAskClientType)
	result, err := m.Process(context.Background(), sess, textM("hello"))
	if err != nil {
		t.Fatal(err)
	}
	// Should stay on same state with retry
	if result.NextState != sm.StateAskClientType {
		t.Errorf("expected ASK_CLIENT_TYPE (retry), got %s", result.NextState)
	}
}

func TestAskClientType_MaxRetries_Escalates(t *testing.T) {
	m := sm.NewMachine()
	RegisterEntityManagementHandlers(m, &testutil.MockEntityRepo{}, &testutil.MockPatientRepo{})

	sess := testSess(sm.StateAskClientType)
	sess.RetryCount = 2 // Already at limit

	result, err := m.Process(context.Background(), sess, textM("invalid"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT on max retries, got %s", result.NextState)
	}
}

// --- SHOW_ENTITY_LIST ---

func TestShowEntityList_WithEntities(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindActiveByCategoryFn: func(ctx context.Context, category string) ([]domain.Entity, error) {
			return []domain.Entity{
				{Code: "EPS001", Name: "NUEVA EPS", Category: "EPS", IsActive: true},
				{Code: "EPS002", Name: "FAMISANAR", Category: "EPS", IsActive: true},
			}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateShowEntityList, showEntityListHandler(entityRepo))

	sess := testSess(sm.StateShowEntityList)
	sess.Context["entity_category"] = "EPS"

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskEntityNumber {
		t.Errorf("expected ASK_ENTITY_NUMBER, got %s", result.NextState)
	}
	// Context is in result.UpdateCtx (not yet applied to session for final interactive state)
	if result.UpdateCtx["entity_list_count"] != "2" {
		t.Errorf("expected entity_list_count=2, got %s", result.UpdateCtx["entity_list_count"])
	}
	codes := result.UpdateCtx["entity_list_codes"]
	if codes != "EPS001,EPS002" {
		t.Errorf("expected entity_list_codes=EPS001,EPS002, got %s", codes)
	}
}

func TestShowEntityList_Empty_Escalates(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindActiveByCategoryFn: func(ctx context.Context, category string) ([]domain.Entity, error) {
			return []domain.Entity{}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateShowEntityList, showEntityListHandler(entityRepo))

	sess := testSess(sm.StateShowEntityList)
	sess.Context["entity_category"] = "SOAT"

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT for empty entities, got %s", result.NextState)
	}
}

func TestShowEntityList_RepoError_Fallback(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindActiveByCategoryFn: func(ctx context.Context, category string) ([]domain.Entity, error) {
			return nil, fmt.Errorf("db error")
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateShowEntityList, showEntityListHandler(entityRepo))

	sess := testSess(sm.StateShowEntityList)
	sess.Context["entity_category"] = "EPS"

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskEntityNumber {
		t.Errorf("expected ASK_ENTITY_NUMBER (fallback), got %s", result.NextState)
	}
}

func TestShowEntityList_SanitasDedup(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindActiveByCategoryFn: func(ctx context.Context, category string) ([]domain.Entity, error) {
			return []domain.Entity{
				{Code: "SAN01", Name: "SANITAS EVENTO", Category: "EPS", IsActive: true},
				{Code: "SAN02", Name: "SANITAS MRC", Category: "EPS", IsActive: true},
				{Code: "EPS003", Name: "FAMISANAR", Category: "EPS", IsActive: true},
			}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateShowEntityList, showEntityListHandler(entityRepo))

	sess := testSess(sm.StateShowEntityList)
	sess.Context["entity_category"] = "EPS"

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskEntityNumber {
		t.Errorf("expected ASK_ENTITY_NUMBER, got %s", result.NextState)
	}
	// SAN01 should be filtered out, leaving SAN02 + FAMISANAR = 2
	if result.UpdateCtx["entity_list_count"] != "2" {
		t.Errorf("expected entity_list_count=2 (SAN01 filtered), got %s", result.UpdateCtx["entity_list_count"])
	}
	codes := result.UpdateCtx["entity_list_codes"]
	if strings.Contains(codes, "SAN01") {
		t.Errorf("SAN01 should be filtered from entity_list_codes, got %s", codes)
	}
	if !strings.Contains(codes, "SAN02") {
		t.Errorf("SAN02 should be in entity_list_codes, got %s", codes)
	}
}

// --- ASK_ENTITY_NUMBER ---

func TestAskEntityNumber_ValidNumber_NonSanitas(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindByCodeFn: func(ctx context.Context, code string) (*domain.Entity, error) {
			return &domain.Entity{Code: code, Name: "NUEVA EPS", IsActive: true}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateAskEntityNumber, askEntityNumberHandler(entityRepo))

	sess := testSess(sm.StateAskEntityNumber)
	sess.Context["entity_list_count"] = "3"
	sess.Context["entity_list_codes"] = "EPS001,EPS002,EPS003"
	sess.Context["entity_category"] = "EPS"

	result, err := m.Process(context.Background(), sess, textM("1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskDocument {
		t.Errorf("expected ASK_DOCUMENT, got %s", result.NextState)
	}
	if result.UpdateCtx["selected_entity_code"] != "EPS001" {
		t.Errorf("expected selected_entity_code=EPS001, got %s", result.UpdateCtx["selected_entity_code"])
	}
}

func TestAskEntityNumber_Sanitas_RedirectToSubmenu(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindByCodeFn: func(ctx context.Context, code string) (*domain.Entity, error) {
			return &domain.Entity{Code: code, Name: "SANITAS", IsActive: true}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateAskEntityNumber, askEntityNumberHandler(entityRepo))

	sess := testSess(sm.StateAskEntityNumber)
	sess.Context["entity_list_count"] = "2"
	sess.Context["entity_list_codes"] = "SAN02,EPS003"
	sess.Context["entity_category"] = "EPS"

	result, err := m.Process(context.Background(), sess, textM("1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskSanitasPlan {
		t.Errorf("expected ASK_SANITAS_PLAN, got %s", result.NextState)
	}
}

func TestAskEntityNumber_InvalidNumber_TooHigh(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateAskEntityNumber, askEntityNumberHandler(nil))

	sess := testSess(sm.StateAskEntityNumber)
	sess.Context["entity_list_count"] = "3"

	result, err := m.Process(context.Background(), sess, textM("99"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskEntityNumber {
		t.Errorf("expected ASK_ENTITY_NUMBER (retry), got %s", result.NextState)
	}
}

func TestAskEntityNumber_NonNumeric(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateAskEntityNumber, askEntityNumberHandler(nil))

	sess := testSess(sm.StateAskEntityNumber)
	sess.Context["entity_list_count"] = "3"

	result, err := m.Process(context.Background(), sess, textM("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskEntityNumber {
		t.Errorf("expected ASK_ENTITY_NUMBER (retry), got %s", result.NextState)
	}
}

// --- ASK_SANITAS_PLAN ---

func TestAskSanitasPlan_Premium(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateAskSanitasPlan, askSanitasPlanHandler())

	sess := testSess(sm.StateAskSanitasPlan)
	result, err := m.Process(context.Background(), sess, postbackM("sanitas_premium"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskDocument {
		t.Errorf("expected ASK_DOCUMENT, got %s", result.NextState)
	}
	if result.UpdateCtx["selected_entity_code"] != "SAN01" {
		t.Errorf("expected selected_entity_code=SAN01, got %s", result.UpdateCtx["selected_entity_code"])
	}
}

func TestAskSanitasPlan_Regular(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateAskSanitasPlan, askSanitasPlanHandler())

	sess := testSess(sm.StateAskSanitasPlan)
	result, err := m.Process(context.Background(), sess, postbackM("sanitas_regular"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskDocument {
		t.Errorf("expected ASK_DOCUMENT, got %s", result.NextState)
	}
	if result.UpdateCtx["selected_entity_code"] != "SAN02" {
		t.Errorf("expected selected_entity_code=SAN02, got %s", result.UpdateCtx["selected_entity_code"])
	}
}

// --- CHECK_ENTITY (legacy) ---

func TestCheckEntity_NoEntityCode(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateCheckEntity, checkEntityHandler(nil))

	sess := testSess(sm.StateCheckEntity)
	// patient_entity is empty

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateChangeEntity {
		t.Errorf("expected CHANGE_ENTITY, got %s", result.NextState)
	}
}

func TestCheckEntity_ActiveEntity(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindByCodeFn: func(ctx context.Context, code string) (*domain.Entity, error) {
			return &domain.Entity{Code: "EPS001", Name: "NUEVA EPS", IsActive: true}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateCheckEntity, checkEntityHandler(entityRepo))

	sess := testSess(sm.StateCheckEntity)
	sess.Context["patient_entity"] = "EPS001"

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateConfirmEntity {
		t.Errorf("expected CONFIRM_ENTITY, got %s", result.NextState)
	}
}

func TestCheckEntity_InactiveEntity(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindByCodeFn: func(ctx context.Context, code string) (*domain.Entity, error) {
			return &domain.Entity{Code: "OLD01", Name: "ENTIDAD ANTIGUA", IsActive: false}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateCheckEntity, checkEntityHandler(entityRepo))

	sess := testSess(sm.StateCheckEntity)
	sess.Context["patient_entity"] = "OLD01"

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateConfirmEntity {
		t.Errorf("expected CONFIRM_ENTITY, got %s", result.NextState)
	}
}

// --- CONFIRM_ENTITY (legacy) ---

func TestConfirmEntity_OK(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateConfirmEntity, confirmEntityHandler())

	sess := testSess(sm.StateConfirmEntity)
	sess.Context["entity_name"] = "NUEVA EPS"

	result, err := m.Process(context.Background(), sess, postbackM("entity_ok"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskMedicalOrder {
		t.Errorf("expected ASK_MEDICAL_ORDER, got %s", result.NextState)
	}
}

func TestConfirmEntity_Change(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateConfirmEntity, confirmEntityHandler())

	sess := testSess(sm.StateConfirmEntity)
	sess.Context["entity_name"] = "NUEVA EPS"

	result, err := m.Process(context.Background(), sess, postbackM("entity_change"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateChangeEntity {
		t.Errorf("expected CHANGE_ENTITY, got %s", result.NextState)
	}
}

// --- CHANGE_ENTITY (legacy) ---

func TestChangeEntity_PostbackSelection(t *testing.T) {
	var updatedEntity string
	patientRepo := &testutil.MockPatientRepo{
		UpdateEntityFn: func(ctx context.Context, patientID, entityCode string) error {
			updatedEntity = entityCode
			return nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateChangeEntity, changeEntityHandler(nil, patientRepo))

	sess := testSess(sm.StateChangeEntity)
	sess.Context["patient_id"] = "PAT-123"

	result, err := m.Process(context.Background(), sess, postbackM("EPS001"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskMedicalOrder {
		t.Errorf("expected ASK_MEDICAL_ORDER, got %s", result.NextState)
	}
	if updatedEntity != "EPS001" {
		t.Errorf("expected UpdateEntity called with EPS001, got %s", updatedEntity)
	}
}

func TestChangeEntity_TextSearch_ExactMatch(t *testing.T) {
	entityRepo := &testutil.MockEntityRepo{
		FindActiveFn: func(ctx context.Context) ([]domain.Entity, error) {
			return []domain.Entity{
				{Code: "SAN02", Name: "SANITAS MRC", IsActive: true},
				{Code: "EPS001", Name: "NUEVA EPS", IsActive: true},
			}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateChangeEntity, changeEntityHandler(entityRepo, &testutil.MockPatientRepo{}))

	sess := testSess(sm.StateChangeEntity)
	sess.Context["patient_id"] = "PAT-123"

	result, err := m.Process(context.Background(), sess, textM("NUEVA"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskMedicalOrder {
		t.Errorf("expected ASK_MEDICAL_ORDER (exact match), got %s", result.NextState)
	}
}
