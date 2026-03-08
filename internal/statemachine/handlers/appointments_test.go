package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/services"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/testutil"
)

// --- Mock appointment service for handler tests ---
type mockApptSvcForHandlers struct {
	upcoming       []domain.Appointment
	upcomingErr    error
	confirmCalled  bool
	cancelCalled   bool
}

func (m *mockApptSvcForHandlers) GetUpcomingAppointments(ctx context.Context, patientID string) ([]domain.Appointment, error) {
	return m.upcoming, m.upcomingErr
}

func (m *mockApptSvcForHandlers) FindConsecutiveBlock(appointments []domain.Appointment, appointmentID string) []domain.Appointment {
	for _, a := range appointments {
		if a.ID == appointmentID {
			return []domain.Appointment{a}
		}
	}
	return nil
}

func (m *mockApptSvcForHandlers) ConfirmBlock(ctx context.Context, block []domain.Appointment, source, channelID string) error {
	m.confirmCalled = true
	return nil
}

func (m *mockApptSvcForHandlers) CancelBlock(ctx context.Context, block []domain.Appointment, reason, source, channelID string) error {
	m.cancelCalled = true
	return nil
}

// We need to test with the real service since handlers use *services.AppointmentService directly.
// Instead, we test the exported handlers through the machine with nil-safe patterns.

func TestFetchAppointments_WithAppts(t *testing.T) {
	// Create mock repos for the appointment service
	appts := []domain.Appointment{
		testutil.SampleAppointment(time.Now().AddDate(0, 0, 3)),
	}

	apptRepo := &testutil.MockAppointmentRepo{
		FindUpcomingByPatientFn: func(ctx context.Context, patientID string) ([]domain.Appointment, error) {
			return appts, nil
		},
	}
	apptSvc := services.NewAppointmentService(apptRepo, nil)

	m := sm.NewMachine()
	RegisterAppointmentHandlers(m, apptSvc, nil)

	sess := testSess(sm.StateFetchAppointments)
	sess.Context["patient_id"] = "PAT001"
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateListAppointments {
		t.Errorf("expected LIST_APPOINTMENTS, got %s", result.NextState)
	}
}

func TestFetchAppointments_NoAppts(t *testing.T) {
	apptRepo := &testutil.MockAppointmentRepo{
		FindUpcomingByPatientFn: func(ctx context.Context, patientID string) ([]domain.Appointment, error) {
			return []domain.Appointment{}, nil
		},
	}
	apptSvc := services.NewAppointmentService(apptRepo, nil)

	m := sm.NewMachine()
	RegisterAppointmentHandlers(m, apptSvc, nil)

	sess := testSess(sm.StateFetchAppointments)
	sess.Context["patient_id"] = "PAT001"
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StatePostActionMenu {
		t.Errorf("expected POST_ACTION_MENU, got %s", result.NextState)
	}
}

func TestFetchAppointments_Error(t *testing.T) {
	apptRepo := &testutil.MockAppointmentRepo{
		FindUpcomingByPatientFn: func(ctx context.Context, patientID string) ([]domain.Appointment, error) {
			return nil, context.DeadlineExceeded
		},
	}
	apptSvc := services.NewAppointmentService(apptRepo, nil)

	m := sm.NewMachine()
	RegisterAppointmentHandlers(m, apptSvc, nil)

	sess := testSess(sm.StateFetchAppointments)
	sess.Context["patient_id"] = "PAT001"
	result, err := m.Process(context.Background(), sess, textM(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StatePostActionMenu {
		t.Errorf("expected POST_ACTION_MENU on error, got %s", result.NextState)
	}
}

func TestListAppointments_Selection(t *testing.T) {
	appt := testutil.SampleAppointment(time.Now().AddDate(0, 0, 3))
	appts := []domain.Appointment{appt}
	apptsJSON, _ := json.Marshal(appts)

	apptRepo := &testutil.MockAppointmentRepo{}
	apptSvc := services.NewAppointmentService(apptRepo, nil)

	m := sm.NewMachine()
	RegisterAppointmentHandlers(m, apptSvc, nil)

	sess := testSess(sm.StateListAppointments)
	sess.Context["appointments_json"] = string(apptsJSON)

	result, err := m.Process(context.Background(), sess, postbackM(appt.ID))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateAppointmentAction {
		t.Errorf("expected APPOINTMENT_ACTION, got %s", result.NextState)
	}
}

func TestAppointmentAction_Confirm(t *testing.T) {
	appt := testutil.SampleAppointment(time.Now().AddDate(0, 0, 3))
	appts := []domain.Appointment{appt}
	apptsJSON, _ := json.Marshal(appts)

	confirmCalled := false
	apptRepo := &testutil.MockAppointmentRepo{
		ConfirmFn: func(ctx context.Context, id, source, chID string) error {
			confirmCalled = true
			return nil
		},
	}
	apptSvc := services.NewAppointmentService(apptRepo, nil)

	m := sm.NewMachine()
	RegisterAppointmentHandlers(m, apptSvc, nil)

	sess := testSess(sm.StateAppointmentAction)
	sess.Context["appointments_json"] = string(apptsJSON)
	sess.Context["selected_appointment_id"] = appt.ID

	result, err := m.Process(context.Background(), sess, postbackM("appt_confirm"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StatePostActionMenu {
		t.Errorf("expected POST_ACTION_MENU, got %s", result.NextState)
	}
	if !confirmCalled {
		t.Error("expected confirm to be called")
	}
}

func TestAppointmentAction_Cancel(t *testing.T) {
	appt := testutil.SampleAppointment(time.Now().AddDate(0, 0, 3))
	appts := []domain.Appointment{appt}
	apptsJSON, _ := json.Marshal(appts)

	cancelCalled := false
	apptRepo := &testutil.MockAppointmentRepo{
		CancelFn: func(ctx context.Context, id, reason, source, chID string) error {
			cancelCalled = true
			return nil
		},
	}
	apptSvc := services.NewAppointmentService(apptRepo, nil)

	m := sm.NewMachine()
	RegisterAppointmentHandlers(m, apptSvc, nil)

	sess := testSess(sm.StateAppointmentAction)
	sess.Context["appointments_json"] = string(apptsJSON)
	sess.Context["selected_appointment_id"] = appt.ID

	result, err := m.Process(context.Background(), sess, postbackM("appt_cancel"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StatePostActionMenu {
		t.Errorf("expected POST_ACTION_MENU, got %s", result.NextState)
	}
	if !cancelCalled {
		t.Error("expected cancel to be called")
	}
}

func TestAppointmentAction_Back(t *testing.T) {
	appt := testutil.SampleAppointment(time.Now().AddDate(0, 0, 3))
	appts := []domain.Appointment{appt}
	apptsJSON, _ := json.Marshal(appts)

	apptRepo := &testutil.MockAppointmentRepo{}
	apptSvc := services.NewAppointmentService(apptRepo, nil)

	m := sm.NewMachine()
	RegisterAppointmentHandlers(m, apptSvc, nil)

	sess := testSess(sm.StateAppointmentAction)
	sess.Context["appointments_json"] = string(apptsJSON)
	sess.Context["selected_appointment_id"] = appt.ID

	result, err := m.Process(context.Background(), sess, postbackM("appt_back"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != sm.StateListAppointments {
		t.Errorf("expected LIST_APPOINTMENTS, got %s", result.NextState)
	}
}
