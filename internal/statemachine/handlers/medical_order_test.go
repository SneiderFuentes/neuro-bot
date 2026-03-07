package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/services"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
)

// --- Mock OCR service ---
type mockOCRServer struct {
	handler http.HandlerFunc
}

func newMockOCRServer(response string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(response))
	}))
}

// --- Mock procedure repo ---
type mockProcedureRepo struct {
	findByCodeFn   func(ctx context.Context, code string) (*domain.Procedure, error)
	searchByNameFn func(ctx context.Context, name string) ([]domain.Procedure, error)
}

func (m *mockProcedureRepo) FindByCode(ctx context.Context, code string) (*domain.Procedure, error) {
	if m.findByCodeFn != nil {
		return m.findByCodeFn(ctx, code)
	}
	return nil, nil
}

func (m *mockProcedureRepo) SearchByName(ctx context.Context, name string) ([]domain.Procedure, error) {
	if m.searchByNameFn != nil {
		return m.searchByNameFn(ctx, name)
	}
	return nil, nil
}

func (m *mockProcedureRepo) FindByID(ctx context.Context, id int) (*domain.Procedure, error) {
	return nil, nil
}

func (m *mockProcedureRepo) FindAllActive(ctx context.Context) ([]domain.Procedure, error) {
	return nil, nil
}

// wrapOpenAIResponse wraps a content string into a full OpenAI API response JSON.
func wrapOpenAIResponse(content string) string {
	resp := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"content": content}},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// --- Tests ---

func registerAskMedicalOrderConfig(m *sm.Machine) {
	m.RegisterWithConfig(sm.StateAskMedicalOrder, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"order_photo", "order_manual"},
		Handler:   askMedicalOrderHandler(),
	})
}

func TestAskMedicalOrder_Photo(t *testing.T) {
	m := sm.NewMachine()
	registerAskMedicalOrderConfig(m)

	sess := testSess(sm.StateAskMedicalOrder)
	result, err := m.Process(context.Background(), sess, postbackM("order_photo"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateUploadMedicalOrder {
		t.Errorf("expected UPLOAD_MEDICAL_ORDER, got %s", result.NextState)
	}
}

func TestAskMedicalOrder_Manual(t *testing.T) {
	m := sm.NewMachine()
	registerAskMedicalOrderConfig(m)

	sess := testSess(sm.StateAskMedicalOrder)
	result, err := m.Process(context.Background(), sess, postbackM("order_manual"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskManualCups {
		t.Errorf("expected ASK_MANUAL_CUPS, got %s", result.NextState)
	}
}

func TestAskMedicalOrder_Invalid(t *testing.T) {
	m := sm.NewMachine()
	registerAskMedicalOrderConfig(m)

	sess := testSess(sm.StateAskMedicalOrder)
	result, err := m.Process(context.Background(), sess, textM("hola"))
	if err != nil {
		t.Fatal(err)
	}
	// Should stay in same state (retry)
	if result.NextState != sm.StateAskMedicalOrder {
		t.Errorf("expected ASK_MEDICAL_ORDER (retry), got %s", result.NextState)
	}
}

func TestUploadMedicalOrder_TextReceived(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(nil))

	sess := testSess(sm.StateUploadMedicalOrder)
	result, err := m.Process(context.Background(), sess, textM("hello"))
	if err != nil {
		t.Fatal(err)
	}
	// Should stay in same state asking for image
	if result.NextState != sm.StateUploadMedicalOrder {
		t.Errorf("expected UPLOAD_MEDICAL_ORDER, got %s", result.NextState)
	}
}

func TestUploadMedicalOrder_RetryPostback(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(nil))

	sess := testSess(sm.StateUploadMedicalOrder)
	result, err := m.Process(context.Background(), sess, postbackM("retry_photo"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateUploadMedicalOrder {
		t.Errorf("expected UPLOAD_MEDICAL_ORDER, got %s", result.NextState)
	}
}

func TestUploadMedicalOrder_GoManualPostback(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(nil))

	sess := testSess(sm.StateUploadMedicalOrder)
	result, err := m.Process(context.Background(), sess, postbackM("go_manual"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskManualCups {
		t.Errorf("expected ASK_MANUAL_CUPS, got %s", result.NextState)
	}
}

func TestAskManualCups_TooShort(t *testing.T) {
	procRepo := &mockProcedureRepo{}
	m := sm.NewMachine()
	m.Register(sm.StateAskManualCups, askManualCupsHandler(procRepo))

	sess := testSess(sm.StateAskManualCups)
	result, err := m.Process(context.Background(), sess, textM("ab"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskManualCups {
		t.Errorf("expected ASK_MANUAL_CUPS (retry), got %s", result.NextState)
	}
}

func TestAskManualCups_SingleResult(t *testing.T) {
	procRepo := &mockProcedureRepo{
		searchByNameFn: func(ctx context.Context, name string) ([]domain.Procedure, error) {
			return []domain.Procedure{
				{ID: 1, Code: "890271", Name: "Electromiografia", RequiredSpaces: 2},
			}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateAskManualCups, askManualCupsHandler(procRepo))

	sess := testSess(sm.StateAskManualCups)
	result, err := m.Process(context.Background(), sess, textM("electro"))
	if err != nil {
		t.Fatal(err)
	}
	// Single result → auto-select → next state
	if result.NextState != sm.StateCheckSpecialCups {
		t.Errorf("expected CHECK_SPECIAL_CUPS, got %s", result.NextState)
	}
	// Check context was set
	found := false
	for k, v := range result.UpdateCtx {
		if k == "cups_code" && v == "890271" {
			found = true
		}
	}
	if !found {
		t.Error("expected cups_code=890271 in UpdateCtx")
	}
}

func TestAskManualCups_MultipleResults(t *testing.T) {
	procRepo := &mockProcedureRepo{
		searchByNameFn: func(ctx context.Context, name string) ([]domain.Procedure, error) {
			return []domain.Procedure{
				{ID: 1, Code: "890271", Name: "Electromiografia"},
				{ID: 2, Code: "890272", Name: "Electroencefalograma"},
			}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateAskManualCups, askManualCupsHandler(procRepo))

	sess := testSess(sm.StateAskManualCups)
	result, err := m.Process(context.Background(), sess, textM("electro"))
	if err != nil {
		t.Fatal(err)
	}
	// Multiple results → show list → SELECT_PROCEDURE
	if result.NextState != sm.StateSelectProcedure {
		t.Errorf("expected SELECT_PROCEDURE, got %s", result.NextState)
	}
}

func TestAskManualCups_NoResults(t *testing.T) {
	procRepo := &mockProcedureRepo{
		searchByNameFn: func(ctx context.Context, name string) ([]domain.Procedure, error) {
			return []domain.Procedure{}, nil
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateAskManualCups, askManualCupsHandler(procRepo))

	sess := testSess(sm.StateAskManualCups)
	result, err := m.Process(context.Background(), sess, textM("xyzabc"))
	if err != nil {
		t.Fatal(err)
	}
	// No results → stay in same state
	if result.NextState != sm.StateAskManualCups {
		t.Errorf("expected ASK_MANUAL_CUPS, got %s", result.NextState)
	}
}

func TestSelectProcedure_ValidPostback(t *testing.T) {
	procs := []struct {
		ID             int    `json:"ID"`
		Code           string `json:"Code"`
		Name           string `json:"Name"`
		ServiceName    string `json:"ServiceName"`
		RequiredSpaces int    `json:"RequiredSpaces"`
	}{
		{ID: 10, Code: "890271", Name: "Electromiografia", ServiceName: "Neurofisiologia", RequiredSpaces: 2},
	}
	procsJSON, _ := json.Marshal(procs)

	m := sm.NewMachine()
	m.Register(sm.StateSelectProcedure, selectProcedureHandler())

	sess := testSess(sm.StateSelectProcedure)
	sess.Context["search_procedures_json"] = string(procsJSON)

	msg := bird.InboundMessage{
		ID: "msg-1", Phone: "+573001234567",
		MessageType: "text", IsPostback: true,
		PostbackPayload: fmt.Sprintf("%d", 10), Text: "10",
	}
	result, err := m.Process(context.Background(), sess, msg)
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateCheckSpecialCups {
		t.Errorf("expected CHECK_SPECIAL_CUPS, got %s", result.NextState)
	}
}

func TestSelectProcedure_TextFallback(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateSelectProcedure, selectProcedureHandler())

	sess := testSess(sm.StateSelectProcedure)
	result, err := m.Process(context.Background(), sess, textM("resonancia"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskManualCups {
		t.Errorf("expected ASK_MANUAL_CUPS, got %s", result.NextState)
	}
}

func TestConfirmOCRResult_Correct(t *testing.T) {
	// Single CUPS → calls AI for accurate espacios
	groupResp := `[{"service": "Fisiatria", "cups": [{"cups_code": "890271", "cups_name": "Electromiografia", "quantity": 1}], "espacios": 1}]`
	srv := newMockOCRServer(wrapOpenAIResponse(groupResp))
	defer srv.Close()
	ocrSvc := services.NewOCRServiceForTest(srv.URL)

	cups := []services.CUPSEntry{{Code: "890271", Name: "Electromiografia", Quantity: 1}}
	cupsJSON, _ := json.Marshal(cups)

	m := sm.NewMachine()
	m.Register(sm.StateConfirmOCRResult, confirmOCRResultHandler(ocrSvc))

	sess := testSess(sm.StateConfirmOCRResult)
	sess.Context["ocr_cups_json"] = string(cupsJSON)

	result, err := m.Process(context.Background(), sess, postbackM("ocr_correct"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateCheckSpecialCups {
		t.Errorf("expected CHECK_SPECIAL_CUPS, got %s", result.NextState)
	}
}

func TestConfirmOCRResult_Incorrect(t *testing.T) {
	ocrSvc := services.NewOCRServiceForTest("http://unused")

	m := sm.NewMachine()
	m.Register(sm.StateConfirmOCRResult, confirmOCRResultHandler(ocrSvc))

	sess := testSess(sm.StateConfirmOCRResult)
	result, err := m.Process(context.Background(), sess, postbackM("ocr_incorrect"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAskManualCups {
		t.Errorf("expected ASK_MANUAL_CUPS, got %s", result.NextState)
	}
}

// --- uploadMedicalOrderHandler image tests ---

func imageMsg(url string) bird.InboundMessage {
	return bird.InboundMessage{
		ID:          "msg-img",
		Phone:       "+573001234567",
		MessageType: "image",
		ImageURL:    url,
		ReceivedAt:  time.Now(),
	}
}

func TestUploadMedicalOrder_ImageSuccess(t *testing.T) {
	ocrResponse := `{"choices":[{"message":{"content":"{\"cups\":[{\"cups_code\":\"890271\",\"cups_name\":\"EMG\",\"quantity\":1}],\"entity\":\"\",\"error\":\"\"}"}}]}`
	srv := newMockOCRServer(ocrResponse)
	defer srv.Close()
	ocrSvc := services.NewOCRServiceForTest(srv.URL)

	m := sm.NewMachine()
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(ocrSvc))

	sess := testSess(sm.StateUploadMedicalOrder)
	result, err := m.Process(context.Background(), sess, imageMsg("https://example.com/order.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateValidateOCR {
		t.Errorf("expected VALIDATE_OCR, got %s", result.NextState)
	}
	if result.UpdateCtx["ocr_cups_json"] == "" {
		t.Error("expected ocr_cups_json to be set")
	}
}

func TestUploadMedicalOrder_ImageEmptyURL(t *testing.T) {
	ocrSvc := services.NewOCRServiceForTest("http://unused")

	m := sm.NewMachine()
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(ocrSvc))

	sess := testSess(sm.StateUploadMedicalOrder)
	result, err := m.Process(context.Background(), sess, imageMsg(""))
	if err != nil {
		t.Fatal(err)
	}
	// Should stay in same state
	if result.NextState != sm.StateUploadMedicalOrder {
		t.Errorf("expected UPLOAD_MEDICAL_ORDER (retry), got %s", result.NextState)
	}
}

func TestUploadMedicalOrder_ImageOCRError(t *testing.T) {
	// Server returns 500 to simulate OCR error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()
	ocrSvc := services.NewOCRServiceForTest(srv.URL)

	m := sm.NewMachine()
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(ocrSvc))

	sess := testSess(sm.StateUploadMedicalOrder)
	result, err := m.Process(context.Background(), sess, imageMsg("https://example.com/order.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	// Should stay in same state with buttons
	if result.NextState != sm.StateUploadMedicalOrder {
		t.Errorf("expected UPLOAD_MEDICAL_ORDER (retry), got %s", result.NextState)
	}
}

func TestUploadMedicalOrder_ImageNoCups(t *testing.T) {
	ocrResponse := `{"choices":[{"message":{"content":"{\"cups\":[],\"entity\":\"\",\"error\":\"\"}"}}]}`
	srv := newMockOCRServer(ocrResponse)
	defer srv.Close()
	ocrSvc := services.NewOCRServiceForTest(srv.URL)

	m := sm.NewMachine()
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(ocrSvc))

	sess := testSess(sm.StateUploadMedicalOrder)
	result, err := m.Process(context.Background(), sess, imageMsg("https://example.com/order.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	// No CUPS found -> stay in same state with buttons
	if result.NextState != sm.StateUploadMedicalOrder {
		t.Errorf("expected UPLOAD_MEDICAL_ORDER (retry), got %s", result.NextState)
	}
}

func TestUploadMedicalOrder_StoresDocument(t *testing.T) {
	// OCR returns document "19262024" in the response
	ocrResponse := `{"choices":[{"message":{"content":"{\"cups\":[{\"cups_code\":\"890271\",\"cups_name\":\"EMG\",\"quantity\":1}],\"entity\":\"\",\"error\":\"\",\"documento\":\"19262024\"}"}}]}`
	srv := newMockOCRServer(ocrResponse)
	defer srv.Close()
	ocrSvc := services.NewOCRServiceForTest(srv.URL)

	m := sm.NewMachine()
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(ocrSvc))

	sess := testSess(sm.StateUploadMedicalOrder)
	result, err := m.Process(context.Background(), sess, imageMsg("https://example.com/order.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateValidateOCR {
		t.Errorf("expected VALIDATE_OCR, got %s", result.NextState)
	}
	if result.UpdateCtx["ocr_document"] != "19262024" {
		t.Errorf("expected ocr_document=19262024, got %q", result.UpdateCtx["ocr_document"])
	}
}

// --- validateOCRHandler tests ---

func TestValidateOCR_EnrichesFromDB(t *testing.T) {
	procRepo := &mockProcedureRepo{
		findByCodeFn: func(ctx context.Context, code string) (*domain.Procedure, error) {
			if code == "890271" {
				return &domain.Procedure{ID: 1, Code: "890271", Name: "Electromiografia de 4 extremidades"}, nil
			}
			return nil, nil
		},
	}

	cups := []services.CUPSEntry{{Code: "890271", Name: "EMG", Quantity: 1}}
	cupsJSON, _ := json.Marshal(cups)

	m := sm.NewMachine()
	m.Register(sm.StateValidateOCR, validateOCRHandler(procRepo))

	sess := testSess(sm.StateValidateOCR)
	sess.Context["ocr_cups_json"] = string(cupsJSON)

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateConfirmOCRResult {
		t.Errorf("expected CONFIRM_OCR_RESULT, got %s", result.NextState)
	}
	// Verify the name was enriched from DB
	var enriched []services.CUPSEntry
	if err := json.Unmarshal([]byte(result.UpdateCtx["ocr_cups_json"]), &enriched); err != nil {
		t.Fatalf("failed to unmarshal enriched cups: %v", err)
	}
	if len(enriched) != 1 || enriched[0].Name != "Electromiografia de 4 extremidades" {
		t.Errorf("expected enriched name='Electromiografia de 4 extremidades', got %q", enriched[0].Name)
	}
}

func TestValidateOCR_DocumentMismatch(t *testing.T) {
	procRepo := &mockProcedureRepo{
		findByCodeFn: func(ctx context.Context, code string) (*domain.Procedure, error) {
			return &domain.Procedure{ID: 1, Code: code, Name: "Procedimiento Test"}, nil
		},
	}

	cups := []services.CUPSEntry{{Code: "890271", Name: "Test", Quantity: 1}}
	cupsJSON, _ := json.Marshal(cups)

	m := sm.NewMachine()
	m.Register(sm.StateValidateOCR, validateOCRHandler(procRepo))

	sess := testSess(sm.StateValidateOCR)
	sess.Context["ocr_cups_json"] = string(cupsJSON)
	sess.Context["ocr_document"] = "19262024"
	sess.Context["patient_document"] = "98765432"

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateConfirmOCRResult {
		t.Errorf("expected CONFIRM_OCR_RESULT, got %s", result.NextState)
	}
	// Check that the result message contains "no coincide" warning
	foundWarning := false
	for _, msg := range result.Messages {
		if bm, ok := msg.(*sm.ButtonMessage); ok {
			if strings.Contains(bm.Text, "no coincide") {
				foundWarning = true
			}
		}
	}
	if !foundWarning {
		t.Error("expected warning message containing 'no coincide' for document mismatch")
	}
}

func TestValidateOCR_DocumentMatch(t *testing.T) {
	procRepo := &mockProcedureRepo{
		findByCodeFn: func(ctx context.Context, code string) (*domain.Procedure, error) {
			return &domain.Procedure{ID: 1, Code: code, Name: "Procedimiento Test"}, nil
		},
	}

	cups := []services.CUPSEntry{{Code: "890271", Name: "Test", Quantity: 1}}
	cupsJSON, _ := json.Marshal(cups)

	m := sm.NewMachine()
	m.Register(sm.StateValidateOCR, validateOCRHandler(procRepo))

	sess := testSess(sm.StateValidateOCR)
	sess.Context["ocr_cups_json"] = string(cupsJSON)
	sess.Context["ocr_document"] = "19262024"
	sess.Context["patient_document"] = "19262024"

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateConfirmOCRResult {
		t.Errorf("expected CONFIRM_OCR_RESULT, got %s", result.NextState)
	}
	// Check that NO warning about document mismatch appears
	for _, msg := range result.Messages {
		if bm, ok := msg.(*sm.ButtonMessage); ok {
			if strings.Contains(bm.Text, "no coincide") {
				t.Error("did not expect 'no coincide' warning when documents match")
			}
		}
	}
}

// --- ocrFailedHandler test ---

func TestOCRFailed_RedirectsToUpload(t *testing.T) {
	m := sm.NewMachine()
	m.Register(sm.StateOCRFailed, ocrFailedHandler())

	sess := testSess(sm.StateOCRFailed)

	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateUploadMedicalOrder {
		t.Errorf("expected UPLOAD_MEDICAL_ORDER, got %s", result.NextState)
	}
}

// --- askManualCupsHandler search error test ---

func TestAskManualCups_SearchError(t *testing.T) {
	procRepo := &mockProcedureRepo{
		searchByNameFn: func(ctx context.Context, name string) ([]domain.Procedure, error) {
			return nil, fmt.Errorf("database timeout")
		},
	}

	m := sm.NewMachine()
	m.Register(sm.StateAskManualCups, askManualCupsHandler(procRepo))

	sess := testSess(sm.StateAskManualCups)
	result, err := m.Process(context.Background(), sess, textM("electro"))
	if err != nil {
		t.Fatal(err)
	}
	// Error -> stays in same state
	if result.NextState != sm.StateAskManualCups {
		t.Errorf("expected ASK_MANUAL_CUPS (retry), got %s", result.NextState)
	}
}

func TestConfirmOCRResult_PreservesSedation(t *testing.T) {
	// OCR detected is_sedated=true for one procedure. GroupByService AI
	// doesn't return is_sedated, so confirmOCRResult must restore it.
	groupResp := `[{"service": "Resonancia", "cups": [{"cups_code": "883210", "cups_name": "RM cerebro", "quantity": 1}], "espacios": 2}]`
	srv := newMockOCRServer(wrapOpenAIResponse(groupResp))
	defer srv.Close()
	ocrSvc := services.NewOCRServiceForTest(srv.URL)

	cups := []services.CUPSEntry{
		{Code: "883210", Name: "RM cerebro bajo sedacion", Quantity: 1, IsSedated: true},
	}
	cupsJSON, _ := json.Marshal(cups)

	m := sm.NewMachine()
	m.Register(sm.StateConfirmOCRResult, confirmOCRResultHandler(ocrSvc))

	sess := testSess(sm.StateConfirmOCRResult)
	sess.Context["ocr_cups_json"] = string(cupsJSON)

	result, err := m.Process(context.Background(), sess, postbackM("ocr_correct"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateCheckSpecialCups {
		t.Errorf("expected CHECK_SPECIAL_CUPS, got %s", result.NextState)
	}
	// ocr_is_sedated should be propagated from the original OCR data
	if result.UpdateCtx["ocr_is_sedated"] != "1" {
		t.Errorf("expected ocr_is_sedated=1, got %q", result.UpdateCtx["ocr_is_sedated"])
	}
	// procedures_json should also have is_sedated preserved
	var groups []services.CUPSGroup
	if err := json.Unmarshal([]byte(result.UpdateCtx["procedures_json"]), &groups); err != nil {
		t.Fatalf("failed to unmarshal procedures_json: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Cups) != 1 {
		t.Fatalf("expected 1 group with 1 cup")
	}
	if !groups[0].Cups[0].IsSedated {
		t.Error("expected IsSedated=true in procedures_json after GroupByService")
	}
}
