package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/neuro-bot/neuro-bot/internal/session"
)

// startSelfReschedule creates a new session at SEARCH_SLOTS pre-populated with
// the old appointment's data. The state machine handles slot search → selection → booking.
// When skipCancel is true (cancellation flow), the old appointment is NOT cancelled
// after the new one is created (it was already cancelled by the admin).
func (m *NotificationManager) startSelfReschedule(phone string, pending *PendingNotification, skipCancel bool) {
	ctx := context.Background()

	// 1. Fetch old appointment + consecutive block
	appt, block, err := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)
	if err != nil || appt == nil {
		slog.Error("self_reschedule: find appointment", "error", err, "appointment_id", pending.AppointmentID)
		m.birdClient.SendText(phone, pending.ConversationID,
			"No pudimos encontrar tu cita. Por favor contacta a la clinica.")
		return
	}

	// 2. Verify session dependencies are available
	if m.sessionRepo == nil || m.workerPool == nil {
		slog.Error("self_reschedule: missing session/worker dependencies")
		m.birdClient.SendText(phone, pending.ConversationID,
			"Servicio temporalmente no disponible. Por favor intenta mas tarde.")
		return
	}

	// 3. Extract procedure info
	cupsCode := ""
	cupsName := ""
	if len(appt.Procedures) > 0 {
		cupsCode = appt.Procedures[0].CupCode
		cupsName = appt.Procedures[0].CupName
	}
	if cupsCode == "" {
		slog.Error("self_reschedule: no procedure on appointment", "appointment_id", pending.AppointmentID)
		m.birdClient.SendText(phone, pending.ConversationID,
			"No pudimos identificar el procedimiento de tu cita. Un agente te ayudara.")
		if pending.ConversationID != "" {
			m.birdClient.UpdateFeedItem(pending.ConversationID, pending.BirdMessageID,
				false, m.cfg.BirdTeamFallback, "")
		}
		return
	}

	// 4. Derive flags from Observations
	isContrasted := "0"
	if strings.Contains(appt.Observations, "Contrastada") {
		isContrasted = "1"
	}
	isSedated := "0"
	if strings.Contains(appt.Observations, "Sedacion") {
		isSedated = "1"
	}

	skipCancelStr := "0"
	if skipCancel {
		skipCancelStr = "1"
	}

	// 5. Create session at SEARCH_SLOTS with pre-populated context
	sess := &session.Session{
		ID:           uuid.New().String(),
		PhoneNumber:  phone,
		CurrentState: "SEARCH_SLOTS",
		Status:       session.StatusActive,
		ExpiresAt:    time.Now().Add(120 * time.Minute),
	}

	sessionCtx := map[string]string{
		// Patient data
		"patient_id":     appt.PatientID,
		"patient_name":   appt.PatientName,
		"patient_entity": appt.Entity,
		"patient_age":    "0", // Skip age restrictions (already validated)

		// Procedure data
		"cups_code":     cupsCode,
		"cups_name":     cupsName,
		"is_contrasted": isContrasted,
		"is_sedated":    isSedated,
		"espacios":      fmt.Sprintf("%d", len(block)),

		// Flow control
		"total_procedures":      "1",
		"current_procedure_idx": "0",
		"menu_option":           "agendar",

		// Reschedule-specific keys
		"reschedule_appt_id":         pending.AppointmentID,
		"reschedule_skip_cancel":     skipCancelStr,
		"reschedule_conversation_id": pending.ConversationID,
		"reschedule_bird_msg_id":     pending.BirdMessageID,

		// Prefer same doctor
		"preferred_doctor_doc": appt.DoctorID,
	}

	if err := m.sessionRepo.Create(ctx, sess); err != nil {
		slog.Error("self_reschedule: create session", "error", err)
		m.birdClient.SendText(phone, pending.ConversationID,
			"Error interno. Por favor intenta mas tarde.")
		return
	}

	if err := m.sessionRepo.SetContextBatch(ctx, sess.ID, sessionCtx); err != nil {
		slog.Error("self_reschedule: set context", "error", err)
		m.birdClient.SendText(phone, pending.ConversationID,
			"Error interno. Por favor intenta mas tarde.")
		return
	}

	// 6. Send "searching..." message and enqueue virtual message
	m.birdClient.SendText(phone, "", "Vamos a buscar horarios disponibles para *"+cupsName+"*...")
	m.workerPool.EnqueueVirtual(phone)

	// 7. Log KPI event
	if m.tracker != nil {
		m.tracker.LogEvent(ctx, sess.ID, phone, "notification_reschedule_self_service",
			map[string]interface{}{
				"appointment_id":    pending.AppointmentID,
				"cups_code":         cupsCode,
				"skip_cancel":       skipCancel,
				"notification_type": pending.Type,
				"conversation_id":   pending.ConversationID,
			})
	}

	slog.Info("self_reschedule session created",
		"phone", phone,
		"appointment_id", pending.AppointmentID,
		"cups_code", cupsCode,
		"skip_cancel", skipCancel,
		"block_size", len(block))
}
