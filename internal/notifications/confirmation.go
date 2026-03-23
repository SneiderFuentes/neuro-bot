package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/utils"
)

// handleConfirmation processes responses to the daily confirmation template.
// NOTE: Caller (HandleResponse) already removed pending from sync.Map and DB via LoadAndDelete.
func (m *NotificationManager) handleConfirmation(phone, action string, pending *PendingNotification) {
	ctx := context.Background()

	switch action {
	case "confirm":
		appt, block, err := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)
		if err != nil || appt == nil {
			slog.Error("confirm: find appointment", "error", err, "appointment_id", pending.AppointmentID)
			return
		}

		if err := m.apptSvc.ConfirmBlock(ctx, block, "whatsapp_bot", pending.ConversationID); err != nil {
			slog.Error("confirm: confirm block", "error", err)
		}

		// Build procedure names
		var procNames []string
		for _, p := range appt.Procedures {
			if p.CupName != "" {
				procNames = append(procNames, p.CupName)
			}
		}
		proceduresText := strings.Join(procNames, " y ")
		if proceduresText == "" {
			proceduresText = "Procedimiento"
		}

		// Build confirmation message with details
		msg := fmt.Sprintf("Tu cita ha sido confirmada!\n\n"+
			"*Fecha:* %s\n"+
			"*Hora:* %s\n"+
			"*Medico:* %s\n"+
			"*Procedimiento:* %s",
			utils.FormatFriendlyDate(appt.Date),
			services.FormatTimeSlot(appt.TimeSlot),
			appt.DoctorName,
			proceduresText,
		)

		// Look up address and preparations from procedure DB
		if m.procRepo != nil && len(appt.Procedures) > 0 {
			var prepText string
			address := ""

			for _, proc := range appt.Procedures {
				if proc.CupCode == "" {
					continue
				}
				p, err := m.procRepo.FindByCode(ctx, proc.CupCode)
				if err != nil || p == nil {
					continue
				}
				if address == "" && p.Address != "" {
					address = p.Address
				}
				if p.Preparation != "" {
					prepText += fmt.Sprintf("\n- Para *%s*: %s", proc.CupName, p.Preparation)
					if p.VideoURL != "" {
						prepText += fmt.Sprintf("\n  📹 Video: %s", p.VideoURL)
					}
					if p.AudioURL != "" {
						prepText += fmt.Sprintf("\n  🎵 Audio: %s", p.AudioURL)
					}
				}
			}

			if address != "" {
				if m.addrMapper != nil {
					msg += "\n" + m.addrMapper.FormatAddress(address)
				} else {
					msg += fmt.Sprintf("\n*Direccion:* %s", address)
				}
			}
			if prepText != "" {
				msg += "\n\n*Preparacion:*" + prepText
			}
		}

		msg += "\n\nRecuerda presentarte 15 minutos antes. Te esperamos!"

		m.birdClient.SendText(phone, pending.ConversationID, msg)

		if pending.ConversationID != "" {
			m.birdClient.UpdateFeedItem(pending.ConversationID, pending.BirdMessageID, true, "", "")
		}

		// Log event
		if m.tracker != nil {
			elapsed := time.Since(pending.CreatedAt).Minutes()
			m.tracker.LogEvent(ctx, "", phone, "notification_confirmed",
				map[string]interface{}{
					"appointment_id":    pending.AppointmentID,
					"response_time_min": int(elapsed),
					"conversation_id":   pending.ConversationID,
				})
		}

		slog.Info("proactive confirmation success",
			"phone", phone,
			"appointment_id", pending.AppointmentID,
			"block_size", len(block))

	case "reschedule":
		m.startConfirmRescheduleSession(phone, pending)

	case "cancel":
		m.startConfirmCancelSession(phone, pending)
	}
}

// handleConfirmationTimeout implements a 4-step escalation chain:
//
//	Step 0 → Follow-up #1 (friendly text, NO agent)
//	Step 1 → Follow-up #2 (direct text, NO agent)
//	Step 2 → Safety escalation (IVR didn't run — escalate to agent)
//	Step 3 → Post-IVR timeout (normal escalation to agent)
//
// NOTE: Caller (handleTimeout) already removed pending from sync.Map and DB via LoadAndDelete.
func (m *NotificationManager) handleConfirmationTimeout(pending *PendingNotification) {
	switch pending.RetryCount {
	case 0:
		// Step 1: Follow-up #1 — friendly text, NO agent assignment
		m.birdClient.SendText(pending.Phone, pending.ConversationID,
			"Hola! Aun no recibimos tu respuesta sobre tu cita de manana. "+
				"Por favor confirma, cancela o reprograma para que podamos gestionar tu espacio.")

		pending.RetryCount = 1
		duration := time.Duration(safeHours(m.cfg.ConfirmFollowup2Hours, 3)) * time.Hour
		pending.Timer = time.AfterFunc(duration, func() { m.handleTimeout(pending.Phone) })
		m.pending.Store(pending.Phone, pending)

		if m.persister != nil {
			expiresAt := time.Now().Add(duration)
			if err := m.persister.Upsert(context.Background(), pending.Phone, pending.Type,
				pending.AppointmentID, pending.WaitingListID, pending.BirdMessageID, pending.ConversationID,
				pending.RetryCount, expiresAt); err != nil {
				slog.Error("persist followup1", "phone", pending.Phone, "error", err)
			}
		}

		slog.Info("confirmation followup 1 sent", "phone", pending.Phone)

	case 1:
		// Step 2: Follow-up #2 — direct text, NO agent assignment
		m.birdClient.SendText(pending.Phone, pending.ConversationID,
			"Recordatorio: Tu cita de manana aun no ha sido confirmada. "+
				"Si no recibimos respuesta, te llamaremos para confirmar.")

		pending.RetryCount = 2
		// Safety-net timer: 2h (wait for 15:00 IVR task) + PostIVR minutes + 30min buffer
		duration := 2*time.Hour + time.Duration(safeMinutes(m.cfg.ConfirmPostIVRMinutes, 30)+30)*time.Minute
		pending.Timer = time.AfterFunc(duration, func() { m.handleTimeout(pending.Phone) })
		m.pending.Store(pending.Phone, pending)

		if m.persister != nil {
			expiresAt := time.Now().Add(duration)
			if err := m.persister.Upsert(context.Background(), pending.Phone, pending.Type,
				pending.AppointmentID, pending.WaitingListID, pending.BirdMessageID, pending.ConversationID,
				pending.RetryCount, expiresAt); err != nil {
				slog.Error("persist followup2", "phone", pending.Phone, "error", err)
			}
		}

		slog.Info("confirmation followup 2 sent", "phone", pending.Phone)

	case 2, 3:
		// Step 3/4: Escalate to agent (step 2 = IVR didn't run, step 3 = post-IVR)
		m.escalateToAgent(pending)
	}
}

// escalateToAgent sends an internal note to Bird Inbox, messages the patient,
// and assigns the conversation to the best available agent. Called as the final
// step of the confirmation escalation chain.
func (m *NotificationManager) escalateToAgent(pending *PendingNotification) {
	ctx := context.Background()

	// 1. Look up appointment details for the note
	appt, _, _ := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)

	patientName := ""
	var note string
	if appt != nil {
		patientName = appt.PatientName
		cupName := services.GetFirstCupName(*appt)
		note = fmt.Sprintf("Paciente %s NO confirmo cita de manana.\n"+
			"Fecha: %s | Hora: %s\n"+
			"Procedimiento: %s\n"+
			"No respondio a: mensajes WhatsApp + llamada IVR.\n"+
			"Cita ID: %s",
			patientName,
			utils.FormatFriendlyDate(appt.Date),
			services.FormatTimeSlot(appt.TimeSlot),
			cupName,
			pending.AppointmentID)
	} else {
		note = fmt.Sprintf("Paciente NO confirmo cita.\nCita ID: %s", pending.AppointmentID)
	}

	// 2. Internal note — visible ONLY in Bird Inbox (patient doesn't see it on WhatsApp)
	if pending.ConversationID != "" {
		m.birdClient.SendInternalText(pending.ConversationID, note)
	}

	// 3. Message to patient
	m.birdClient.SendText(pending.Phone, pending.ConversationID,
		"Un asistente del centro se comunicara contigo para confirmar tu cita de manana.")

	// 4. Assign to best available agent
	if pending.ConversationID != "" {
		m.birdClient.EscalateToAgent(
			pending.ConversationID, pending.Phone,
			m.cfg.BirdTeamFallback, "Call Center",
			patientName, m.cfg.BirdTeamFallback)
	}

	// 5. Log event
	if m.tracker != nil {
		m.tracker.LogEvent(ctx, "", pending.Phone, "notification_escalated_agent",
			map[string]interface{}{
				"appointment_id": pending.AppointmentID,
				"retry_count":    pending.RetryCount,
			})
	}

	slog.Info("confirmation escalated to agent",
		"phone", pending.Phone,
		"appointment_id", pending.AppointmentID,
		"retry_count", pending.RetryCount)
}

// startConfirmRescheduleSession creates a new session at CONFIRM_RESCHEDULE_NOTIF
// so the state machine handles the "1/2" confirmation with proper retry logic.
func (m *NotificationManager) startConfirmRescheduleSession(phone string, pending *PendingNotification) {
	ctx := context.Background()

	appt, block, err := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)
	if err != nil || appt == nil {
		slog.Error("startConfirmRescheduleSession: find appointment", "error", err, "appointment_id", pending.AppointmentID)
		m.birdClient.SendText(phone, pending.ConversationID,
			"No pudimos encontrar tu cita. Por favor contacta a la clinica.")
		return
	}

	if m.sessionRepo == nil || m.workerPool == nil {
		slog.Error("startConfirmRescheduleSession: missing session/worker dependencies")
		m.birdClient.SendText(phone, pending.ConversationID,
			"Servicio temporalmente no disponible. Por favor intenta mas tarde.")
		return
	}

	cupsCode := ""
	cupsName := ""
	if len(appt.Procedures) > 0 {
		cupsCode = appt.Procedures[0].CupCode
		cupsName = appt.Procedures[0].CupName
	}

	isContrasted := "0"
	if strings.Contains(appt.Observations, "Contrastada") {
		isContrasted = "1"
	}
	isSedated := "0"
	if strings.Contains(appt.Observations, "Sedacion") {
		isSedated = "1"
	}

	// Build procedures_json required by createAppointmentHandler
	cups := make([]services.CUPSEntry, 0, len(appt.Procedures))
	for _, p := range appt.Procedures {
		cups = append(cups, services.CUPSEntry{
			Code:     p.CupCode,
			Name:     p.CupName,
			Quantity: 1,
		})
	}
	if len(cups) == 0 && cupsCode != "" {
		cups = []services.CUPSEntry{{Code: cupsCode, Name: cupsName, Quantity: 1}}
	}
	groups := []services.CUPSGroup{{
		ServiceType: "general",
		Cups:        cups,
		Espacios:    len(block),
	}}
	proceduresJSON, _ := json.Marshal(groups)

	sess := &session.Session{
		ID:             uuid.New().String(),
		PhoneNumber:    phone,
		CurrentState:   sm.StateConfirmRescheduleNotif,
		ConversationID: pending.ConversationID,
		Status:         session.StatusActive,
		ExpiresAt:      time.Now().Add(30 * time.Minute),
	}

	sessCtx := map[string]string{
		// Patient data
		"patient_id":     appt.PatientID,
		"patient_name":   appt.PatientName,
		"patient_entity": appt.Entity,
		"patient_age":    "0",

		// Procedure data
		"cups_code":       cupsCode,
		"cups_name":       cupsName,
		"is_contrasted":   isContrasted,
		"is_sedated":      isSedated,
		"espacios":        fmt.Sprintf("%d", len(block)),
		"procedures_json": string(proceduresJSON),

		// Flow control
		"total_procedures":      "1",
		"current_procedure_idx": "0",
		"menu_option":           "agendar",

		// Reschedule keys (used by SEARCH_SLOTS if confirmed)
		"reschedule_appt_id":         pending.AppointmentID,
		"reschedule_skip_cancel":     "0",
		"reschedule_conversation_id": pending.ConversationID,
		"reschedule_bird_msg_id":     pending.BirdMessageID,
		"preferred_doctor_doc":       appt.DoctorID,

		// Display keys for confirmation prompt
		"notif_appt_date": utils.FormatFriendlyDate(appt.Date),
		"notif_appt_time": services.FormatTimeSlot(appt.TimeSlot),
		"notif_cups_name": services.GetFirstCupName(*appt),
	}

	if err := m.sessionRepo.Create(ctx, sess); err != nil {
		slog.Error("startConfirmRescheduleSession: create session", "error", err)
		m.birdClient.SendText(phone, pending.ConversationID, "Error interno. Por favor intenta mas tarde.")
		return
	}
	if err := m.sessionRepo.SetContextBatch(ctx, sess.ID, sessCtx); err != nil {
		slog.Error("startConfirmRescheduleSession: set context", "error", err)
		m.birdClient.SendText(phone, pending.ConversationID, "Error interno. Por favor intenta mas tarde.")
		return
	}

	m.workerPool.EnqueueVirtual(phone)
	slog.Info("confirm_reschedule_session created", "phone", phone, "appointment_id", pending.AppointmentID)
}

// startConfirmCancelSession creates a new session at CONFIRM_CANCEL_NOTIF
// so the state machine handles the "1/2" confirmation with proper retry logic.
func (m *NotificationManager) startConfirmCancelSession(phone string, pending *PendingNotification) {
	ctx := context.Background()

	appt, _, err := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)
	if err != nil || appt == nil {
		slog.Error("startConfirmCancelSession: find appointment", "error", err, "appointment_id", pending.AppointmentID)
		m.birdClient.SendText(phone, pending.ConversationID,
			"No pudimos encontrar tu cita. Por favor contacta a la clinica.")
		return
	}

	if m.sessionRepo == nil || m.workerPool == nil {
		slog.Error("startConfirmCancelSession: missing session/worker dependencies")
		m.birdClient.SendText(phone, pending.ConversationID,
			"Servicio temporalmente no disponible. Por favor intenta mas tarde.")
		return
	}

	sess := &session.Session{
		ID:             uuid.New().String(),
		PhoneNumber:    phone,
		CurrentState:   sm.StateConfirmCancelNotif,
		ConversationID: pending.ConversationID,
		Status:         session.StatusActive,
		ExpiresAt:      time.Now().Add(30 * time.Minute),
	}

	sessCtx := map[string]string{
		"patient_id":      appt.PatientID,
		"patient_name":    appt.PatientName,
		"notif_appt_id":   pending.AppointmentID,
		"notif_appt_date": utils.FormatFriendlyDate(appt.Date),
		"notif_appt_time": services.FormatTimeSlot(appt.TimeSlot),
		"notif_cups_name": services.GetFirstCupName(*appt),
		"notif_bird_msg_id": pending.BirdMessageID,
	}

	if err := m.sessionRepo.Create(ctx, sess); err != nil {
		slog.Error("startConfirmCancelSession: create session", "error", err)
		m.birdClient.SendText(phone, pending.ConversationID, "Error interno. Por favor intenta mas tarde.")
		return
	}
	if err := m.sessionRepo.SetContextBatch(ctx, sess.ID, sessCtx); err != nil {
		slog.Error("startConfirmCancelSession: set context", "error", err)
		m.birdClient.SendText(phone, pending.ConversationID, "Error interno. Por favor intenta mas tarde.")
		return
	}

	m.workerPool.EnqueueVirtual(phone)
	slog.Info("confirm_cancel_session created", "phone", phone, "appointment_id", pending.AppointmentID)
}
