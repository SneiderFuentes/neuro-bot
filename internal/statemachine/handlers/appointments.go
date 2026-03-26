package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/utils"
)

// CancellationCallback is called after a patient cancels an appointment via the bot.
// cupsCode is the CUPS code of the freed slot (called once per unique CUPS in the block).
type CancellationCallback func(ctx context.Context, cupsCode string)

// RegisterAppointmentHandlers registra los handlers del flujo de consulta de citas.
func RegisterAppointmentHandlers(m *sm.Machine, apptSvc *services.AppointmentService, procRepo repository.ProcedureRepository, addrMapper *services.AddressMapper, onCancel CancellationCallback) {
	m.Register(sm.StateFetchAppointments, fetchAppointmentsHandler(apptSvc))
	m.Register(sm.StateListAppointments, listAppointmentsHandler(apptSvc))
	m.Register(sm.StateAppointmentAction, appointmentActionHandler(apptSvc, procRepo, addrMapper))
	m.Register(sm.StateConfirmAppointment, confirmAppointmentHandler(apptSvc, procRepo, addrMapper))
	m.Register(sm.StateCancelAppointment, cancelAppointmentHandler(apptSvc, onCancel))
	m.Register(sm.StateNoAppointments, noAppointmentsHandler())

	// Flujos de confirmación desde notificaciones proactivas
	confirmReschedulePrompt := func(sess *session.Session, result *sm.StateResult) {
		result.WithText(fmt.Sprintf(
			"¿Confirmas que deseas reprogramar tu cita?\n\n"+
				"📅 *Fecha actual:* %s\n"+
				"🕐 *Hora:* %s\n"+
				"💊 *Procedimiento:* %s\n\n"+
				"Responde con el número de tu elección:\n"+
				"*1.* Sí, buscar nuevos horarios\n"+
				"*2.* No, mantener mi cita",
			sess.GetContext("notif_appt_date"),
			sess.GetContext("notif_appt_time"),
			sess.GetContext("notif_cups_name"),
		))
	}
	m.RegisterWithConfig(sm.StateConfirmRescheduleNotif, sm.HandlerConfig{
		InputType:   sm.InputButton,
		Options:     []string{"reschedule_yes", "reschedule_no"},
		ErrorMsg:    "Por favor responde 1 o 2.",
		RetryPrompt: confirmReschedulePrompt,
		Handler:     confirmRescheduleNotifHandler(),
	})

	confirmCancelPrompt := func(sess *session.Session, result *sm.StateResult) {
		result.WithText(fmt.Sprintf(
			"¿Confirmas que deseas cancelar tu cita?\n\n"+
				"📅 *Fecha:* %s\n"+
				"🕐 *Hora:* %s\n"+
				"💊 *Procedimiento:* %s\n\n"+
				"Responde con el número de tu elección:\n"+
				"*1.* Sí, cancelar mi cita\n"+
				"*2.* No, mantener mi cita",
			sess.GetContext("notif_appt_date"),
			sess.GetContext("notif_appt_time"),
			sess.GetContext("notif_cups_name"),
		))
	}
	m.RegisterWithConfig(sm.StateConfirmCancelNotif, sm.HandlerConfig{
		InputType:   sm.InputButton,
		Options:     []string{"cancel_yes", "cancel_no"},
		ErrorMsg:    "Por favor responde 1 o 2.",
		RetryPrompt: confirmCancelPrompt,
		Handler:     confirmCancelNotifHandler(apptSvc, onCancel),
	})
}

// FETCH_APPOINTMENTS (automático) — consulta citas del paciente y muestra la lista
func fetchAppointmentsHandler(apptSvc *services.AppointmentService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		patientID := sess.GetContext("patient_id")

		appointments, err := apptSvc.GetUpcomingAppointments(ctx, patientID)
		if err != nil {
			return buildAutoCloseResult("Error al consultar tus citas. Intenta más tarde.").
				WithEvent("fetch_appointments_error", map[string]interface{}{"error": err.Error()}), nil
		}

		if len(appointments) == 0 {
			return buildAutoCloseResult("No tienes citas pendientes o confirmadas.").
				WithEvent("no_appointments_found", nil), nil
		}

		// Serializar citas en contexto para los siguientes estados
		apptJSON, _ := json.Marshal(appointments)

		// Generar la lista aquí (LIST_APPOINTMENTS es interactivo, no auto-chain)
		listMsg := buildAppointmentList(apptSvc, appointments)

		return sm.NewResult(sm.StateListAppointments).
			WithContext("appointments_json", string(apptJSON)).
			WithList(listMsg.body, listMsg.button, listMsg.section).
			WithEvent("appointments_found", map[string]interface{}{"count": len(appointments)}), nil
	}
}

// LIST_APPOINTMENTS (interactivo, lista) — espera selección de cita, muestra detalle al seleccionar
func listAppointmentsHandler(apptSvc *services.AppointmentService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		// Si es postback con ID de cita seleccionada
		if msg.IsPostback {
			var appts []domain.Appointment
			if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appts); err == nil {
				for _, a := range appts {
					if a.ID == msg.PostbackPayload {
						sess.RetryCount = 0

						// Mostrar detalle + lista de acciones en un solo mensaje
						detail := buildAppointmentDetail(apptSvc, appts, a.ID)
						return sm.NewResult(sm.StateAppointmentAction).
							WithContext("selected_appointment_id", msg.PostbackPayload).
							WithList(detail+"\n\n¿Qué deseas hacer con esta cita?", "Ver opciones",
								sm.ListSection{Title: "Acciones", Rows: appointmentActionRows()},
							).
							WithEvent("appointment_selected", map[string]interface{}{"id": msg.PostbackPayload}), nil
					}
				}
			}
			// Invalid postback ID — fall through to retry + re-show list
		}

		// Selección numérica por agente: /bot resume LIST_APPOINTMENTS 1
		if n, err := strconv.Atoi(strings.TrimSpace(msg.Text)); err == nil && n >= 1 {
			var appts []domain.Appointment
			if jsonErr := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appts); jsonErr == nil && n <= len(appts) {
				selected := appts[n-1]
				sess.RetryCount = 0
				detail := buildAppointmentDetail(apptSvc, appts, selected.ID)
				return sm.NewResult(sm.StateAppointmentAction).
					WithContext("selected_appointment_id", selected.ID).
					WithList(detail+"\n\n¿Qué deseas hacer con esta cita?", "Ver opciones",
						sm.ListSection{Title: "Acciones", Rows: appointmentActionRows()},
					).
					WithEvent("appointment_selected", map[string]interface{}{"id": selected.ID}), nil
			}
		}

		// Texto o postback inválido — retry antes de re-mostrar lista
		result := sm.RetryOrEscalate(sess, "Selecciona una cita de la lista.")
		if result.NextState == sm.StateEscalateToAgent {
			return result, nil
		}

		// Cargar citas del contexto y re-mostrar lista
		var appointments []domain.Appointment
		if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments); err != nil {
			return buildAutoCloseResult("Error al cargar las citas. Intenta de nuevo."), nil
		}

		listMsg := buildAppointmentList(apptSvc, appointments)

		return sm.NewResult(sess.CurrentState).
			WithList(listMsg.body, listMsg.button, listMsg.section).
			WithEvent("appointments_listed", map[string]interface{}{"shown": len(listMsg.section.Rows)}), nil
	}
}

// APPOINTMENT_ACTION (interactivo, lista) — procesa acción seleccionada sobre la cita
func appointmentActionHandler(apptSvc *services.AppointmentService, procRepo repository.ProcedureRepository, addrMapper *services.AddressMapper) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selectedID := sess.GetContext("selected_appointment_id")

		result, selected := sm.ValidateButtonResponse(sess, msg, "appt_confirm", "appt_cancel", "appt_reschedule", "appt_preparation", "appt_back", "appt_menu")
		if result != nil {
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}

			// Re-mostrar detalle + acciones
			var appointments []domain.Appointment
			if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments); err != nil {
				return buildAutoCloseResult("Error al cargar las citas."), nil
			}

			detail := buildAppointmentDetail(apptSvc, appointments, selectedID)
			if detail == "" {
				return sm.NewResult(sm.StateListAppointments).
					WithText("Cita no encontrada. Selecciona otra.").
					WithClearCtx("selected_appointment_id"), nil
			}

			return sm.NewResult(sess.CurrentState).
				WithList(detail+"\n\n¿Qué deseas hacer con esta cita?", "Ver opciones",
					sm.ListSection{Title: "Acciones", Rows: appointmentActionRows()},
				), nil
		}

		switch selected {
		case "appt_confirm":
			return sm.NewResult(sm.StateConfirmAppointment).
				WithButtons("¿Estás seguro de *confirmar* esta cita?",
					sm.Button{Text: "Sí, confirmar", Payload: "confirm_yes"},
					sm.Button{Text: "No, volver", Payload: "confirm_no"},
				).
				WithEvent("appointment_confirm_requested", nil), nil

		case "appt_cancel":
			return sm.NewResult(sm.StateCancelAppointment).
				WithButtons("¿Estás seguro de *cancelar* esta cita? Esta acción no se puede deshacer.",
					sm.Button{Text: "Sí, cancelar", Payload: "cancel_yes"},
					sm.Button{Text: "No, volver", Payload: "cancel_no"},
				).
				WithEvent("appointment_cancel_requested", nil), nil

		case "appt_reschedule":
			// Extraer datos de la cita existente y buscar slots directamente.
			// La cita vieja NO se cancela — solo se cancela cuando se crea la nueva
			// (via reschedule_appt_id en createAppointmentHandler).
			var appointments []domain.Appointment
			json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments)

			var selectedAppt *domain.Appointment
			for i, a := range appointments {
				if a.ID == selectedID {
					selectedAppt = &appointments[i]
					break
				}
			}
			if selectedAppt == nil {
				return sm.NewResult(sm.StateListAppointments).
					WithText("Cita no encontrada."), nil
			}

			cupsCode, cupsName := "", ""
			if len(selectedAppt.Procedures) > 0 {
				cupsCode = selectedAppt.Procedures[0].CupCode
				cupsName = selectedAppt.Procedures[0].CupName
			}
			if cupsCode == "" {
				return buildAutoCloseResult("No se puede reprogramar: no se encontró el procedimiento."), nil
			}

			isContrasted := "0"
			if strings.Contains(selectedAppt.Observations, "Contrastada") {
				isContrasted = "1"
			}
			isSedated := "0"
			if strings.Contains(selectedAppt.Observations, "Sedacion") {
				isSedated = "1"
			}

			block := apptSvc.FindConsecutiveBlock(appointments, selectedID)
			espacios := len(block)
			if espacios == 0 {
				espacios = 1
			}

			return sm.NewResult(sm.StateSearchSlots).
				WithContext("cups_code", cupsCode).
				WithContext("cups_name", cupsName).
				WithContext("is_contrasted", isContrasted).
				WithContext("is_sedated", isSedated).
				WithContext("espacios", fmt.Sprintf("%d", espacios)).
				WithContext("preferred_doctor_doc", selectedAppt.DoctorID).
				WithContext("total_procedures", "1").
				WithContext("current_procedure_idx", "0").
				WithContext("reschedule_appt_id", selectedAppt.ID).
				WithContext("patient_age", "0").
				WithText("Buscando horarios disponibles para reprogramar tu cita de *"+cupsName+"*...").
				WithEvent("appointment_reschedule_started", map[string]interface{}{
					"old_appt_id": selectedAppt.ID,
					"cups_code":   cupsCode,
				}), nil

		case "appt_preparation":
			return showAppointmentPreparation(ctx, sess, apptSvc, procRepo, addrMapper)

		case "appt_back":
			var appointments []domain.Appointment
			json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments)
			listMsg := buildAppointmentList(apptSvc, appointments)

			return sm.NewResult(sm.StateListAppointments).
				WithList(listMsg.body, listMsg.button, listMsg.section).
				WithClearCtx("selected_appointment_id"), nil

		case "appt_menu":
			r := sm.NewResult(sm.StateMainMenu).
				WithClearCtx("selected_appointment_id", "appointments_json")
			r.Messages = append(r.Messages, buildMainMenuList())
			return r.WithEvent("appointment_back_to_menu", nil), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// CONFIRM_APPOINTMENT (interactivo) — reconfirmación antes de confirmar la cita
func confirmAppointmentHandler(apptSvc *services.AppointmentService, procRepo repository.ProcedureRepository, addrMapper *services.AddressMapper) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "confirm_yes", "confirm_no")
		if result != nil {
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}
			result.Messages = nil
			return sm.NewResult(sess.CurrentState).
				WithButtons("¿Estás seguro de *confirmar* esta cita?",
					sm.Button{Text: "Sí, confirmar", Payload: "confirm_yes"},
					sm.Button{Text: "No, volver", Payload: "confirm_no"},
				), nil
		}

		switch selected {
		case "confirm_yes":
			return executeConfirmAppointment(ctx, sess, apptSvc, procRepo, addrMapper)

		case "confirm_no":
			// Volver al detalle de la cita
			return backToAppointmentAction(sess, apptSvc), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// CANCEL_APPOINTMENT (interactivo) — reconfirmación antes de cancelar la cita.
func cancelAppointmentHandler(apptSvc *services.AppointmentService, onCancel CancellationCallback) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "cancel_yes", "cancel_no")
		if result != nil {
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}
			result.Messages = nil
			return sm.NewResult(sess.CurrentState).
				WithButtons("¿Estás seguro de *cancelar* esta cita? Esta acción no se puede deshacer.",
					sm.Button{Text: "Sí, cancelar", Payload: "cancel_yes"},
					sm.Button{Text: "No, volver", Payload: "cancel_no"},
				), nil
		}

		switch selected {
		case "cancel_yes":
			return executeCancelAppointment(ctx, sess, apptSvc, onCancel)

		case "cancel_no":
			return backToAppointmentAction(sess, apptSvc), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// NO_APPOINTMENTS (interactivo) — menú cuando no hay citas
func noAppointmentsHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "no_appt_menu", "no_appt_end")
		if result != nil {
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}
			result.Messages = nil
			return sm.NewResult(sess.CurrentState).
				WithButtons("No tienes citas pendientes o confirmadas.\n\n¿Qué deseas hacer?",
					sm.Button{Text: "Menú principal", Payload: "no_appt_menu"},
					sm.Button{Text: "Terminar chat", Payload: "no_appt_end"},
				), nil
		}

		switch selected {
		case "no_appt_menu":
			r := sm.NewResult(sm.StateMainMenu)
			r.Messages = append(r.Messages, buildMainMenuList())
			return r.WithEvent("no_appt_back_to_menu", nil), nil

		case "no_appt_end":
			return sm.NewResult(sm.StateFarewell).
				WithEvent("no_appt_farewell", nil), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// confirmRescheduleNotifHandler handles CONFIRM_RESCHEDULE_NOTIF state.
// Patient pressed "Reprogramar" on the proactive confirmation template.
// We ask them to confirm (1/2) before launching the slot search flow.
func confirmRescheduleNotifHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		switch selected {
		case "reschedule_yes":
			return sm.NewResult(sm.StateSearchSlots).
				WithEvent("notification_reschedule_confirmed", nil), nil
		default: // reschedule_no
			return sm.NewResult(sm.StateFarewell).
				WithText("Entendido, tu cita queda vigente. ¡Te esperamos!").
				WithEvent("notification_reschedule_declined", nil), nil
		}
	}
}

// confirmCancelNotifHandler handles CONFIRM_CANCEL_NOTIF state.
// Patient pressed "Cancelar" on the proactive confirmation template.
// We ask them to confirm (1/2) before executing the cancellation.
func confirmCancelNotifHandler(apptSvc *services.AppointmentService, onCancel CancellationCallback) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)
		switch selected {
		case "cancel_yes":
			// Get all appointment IDs stored by startConfirmCancelSession
			allIDsJSON := sess.GetContext("notif_appt_ids")
			var allIDs []string
			if allIDsJSON != "" {
				json.Unmarshal([]byte(allIDsJSON), &allIDs)
			}

			// Fallback: if no batch IDs, use the single appointment block
			if len(allIDs) == 0 {
				apptID := sess.GetContext("notif_appt_id")
				appt, block, err := apptSvc.FindBlockByAppointmentID(ctx, apptID)
				if err != nil || appt == nil {
					return sm.NewResult(sm.StateFarewell).
						WithText("No pudimos encontrar tu cita. Por favor contacta a la clínica."), nil
				}
				for _, a := range block {
					allIDs = append(allIDs, a.ID)
				}
			}

			if err := apptSvc.CancelByIDs(ctx, allIDs, "Cancelada por paciente via WhatsApp", "whatsapp", sess.ConversationID); err != nil {
				return sm.NewResult(sm.StateFarewell).
					WithText("Error al cancelar la cita. Por favor contacta a la clínica.").
					WithEvent("notification_cancel_error", map[string]interface{}{"error": err.Error()}), nil
			}

			// Notify waiting list for freed CUPS codes (stored in session context before cancel)
			if onCancel != nil {
				cupsJSON := sess.GetContext("notif_cups_codes")
				if cupsJSON != "" {
					var cupsCodes []string
					json.Unmarshal([]byte(cupsJSON), &cupsCodes)
					for _, code := range cupsCodes {
						go onCancel(ctx, code)
					}
				}
			}

			return sm.NewResult(sm.StateFarewell).
				WithText("Tu cita ha sido cancelada.\n\nSi deseas reagendar, puedes escribirnos cuando lo necesites.").
				WithEvent("notification_cancel_confirmed", map[string]interface{}{
					"appointment_ids": allIDs,
					"total_cancelled": len(allIDs),
				}), nil
		default: // cancel_no
			return sm.NewResult(sm.StateFarewell).
				WithText("Entendido, tu cita queda vigente. ¡Te esperamos!").
				WithEvent("notification_cancel_declined", nil), nil
		}
	}
}

// --- Helpers privados ---

// executeConfirmAppointment realiza la confirmación de la cita, guardando el conversationID como medio.
// Busca preparaciones, video/audio y dirección con Maps URL para enviarlas al paciente.
func executeConfirmAppointment(ctx context.Context, sess *session.Session, apptSvc *services.AppointmentService, procRepo repository.ProcedureRepository, addrMapper *services.AddressMapper) (*sm.StateResult, error) {
	selectedID := sess.GetContext("selected_appointment_id")

	var appointments []domain.Appointment
	if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments); err != nil {
		return buildAutoCloseResult("Error al procesar la cita."), nil
	}

	block := apptSvc.FindConsecutiveBlock(appointments, selectedID)
	if len(block) == 0 {
		return sm.NewResult(sm.StateListAppointments).
			WithText("Cita no encontrada.").
			WithClearCtx("selected_appointment_id"), nil
	}

	if err := apptSvc.ConfirmBlock(ctx, block, "whatsapp", sess.ConversationID); err != nil {
		return buildAutoCloseResult("Error al confirmar la cita. Intenta más tarde.").
			WithEvent("appointment_confirm_error", map[string]interface{}{"error": err.Error()}), nil
	}

	msg := fmt.Sprintf("*Cita confirmada exitosamente!*\n\n%d cita(s) confirmada(s).", len(block))

	// Buscar preparaciones, video/audio y dirección del procedimiento
	if procRepo != nil {
		var prepText string
		address := ""
		for _, a := range block {
			for _, proc := range a.Procedures {
				if proc.CupCode == "" {
					continue
				}
				p, err := procRepo.FindByCode(ctx, proc.CupCode)
				if err != nil || p == nil {
					continue
				}
				if address == "" && p.Address != "" {
					address = p.Address
				}
				if p.Preparation != "" {
					prepText += fmt.Sprintf("\n• Para *%s*: %s", proc.CupName, p.Preparation)
					if p.VideoURL != "" {
						prepText += fmt.Sprintf("\n  📹 Ver video: %s", p.VideoURL)
					}
					if p.AudioURL != "" {
						prepText += fmt.Sprintf("\n  🎵 Audio: %s", p.AudioURL)
					}
				}
			}
		}
		if address != "" {
			if addrMapper != nil {
				msg += "\n" + addrMapper.FormatAddress(address)
			} else {
				msg += fmt.Sprintf("\n*Dirección:* %s", address)
			}
		}
		if prepText != "" {
			msg += "\n\n📋 *Preparación:*" + prepText
		}
	}

	msg += "\n\nRecuerda presentarte 15 minutos antes con tu documento."

	return buildAutoCloseResult(msg).
		WithClearCtx("selected_appointment_id", "appointments_json").
		WithEvent("appointment_confirmed", map[string]interface{}{
			"appointment_id": selectedID,
			"block_size":     len(block),
		}), nil
}

// executeCancelAppointment realiza la cancelación de la cita.
func executeCancelAppointment(ctx context.Context, sess *session.Session, apptSvc *services.AppointmentService, onCancel CancellationCallback) (*sm.StateResult, error) {
	selectedID := sess.GetContext("selected_appointment_id")

	var appointments []domain.Appointment
	if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments); err != nil {
		return buildAutoCloseResult("Error al procesar la cita."), nil
	}

	block := apptSvc.FindConsecutiveBlock(appointments, selectedID)
	if len(block) == 0 {
		return sm.NewResult(sm.StateListAppointments).
			WithText("Cita no encontrada.").
			WithClearCtx("selected_appointment_id"), nil
	}

	if err := apptSvc.CancelBlock(ctx, block, "Cancelada por paciente via WhatsApp", "whatsapp", sess.ConversationID); err != nil {
		return buildAutoCloseResult("Error al cancelar la cita. Intenta más tarde.").
			WithEvent("appointment_cancel_error", map[string]interface{}{"error": err.Error()}), nil
	}

	// Notify waiting list for freed CUPS codes
	if onCancel != nil {
		seen := make(map[string]bool)
		for _, appt := range block {
			for _, proc := range appt.Procedures {
				if proc.CupCode != "" && !seen[proc.CupCode] {
					seen[proc.CupCode] = true
					go onCancel(ctx, proc.CupCode)
				}
			}
		}
	}

	msg := fmt.Sprintf("*Cita cancelada.*\n\n%d cita(s) cancelada(s).", len(block))

	return buildAutoCloseResult(msg).
		WithClearCtx("selected_appointment_id", "appointments_json").
		WithEvent("appointment_cancelled", map[string]interface{}{
			"appointment_id": selectedID,
			"block_size":     len(block),
		}), nil
}

// backToAppointmentAction re-muestra el detalle de la cita + lista de acciones.
func backToAppointmentAction(sess *session.Session, apptSvc *services.AppointmentService) *sm.StateResult {
	selectedID := sess.GetContext("selected_appointment_id")
	var appointments []domain.Appointment
	json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments)

	detail := buildAppointmentDetail(apptSvc, appointments, selectedID)
	if detail == "" {
		return sm.NewResult(sm.StateListAppointments).
			WithText("Cita no encontrada. Selecciona otra.").
			WithClearCtx("selected_appointment_id")
	}

	return sm.NewResult(sm.StateAppointmentAction).
		WithList(detail+"\n\n¿Qué deseas hacer con esta cita?", "Ver opciones",
			sm.ListSection{Title: "Acciones", Rows: appointmentActionRows()},
		)
}

// showAppointmentPreparation looks up preparation instructions for the selected appointment's procedures.
func showAppointmentPreparation(ctx context.Context, sess *session.Session, apptSvc *services.AppointmentService, procRepo repository.ProcedureRepository, addrMapper *services.AddressMapper) (*sm.StateResult, error) {
	selectedID := sess.GetContext("selected_appointment_id")
	var appointments []domain.Appointment
	json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments)

	block := apptSvc.FindConsecutiveBlock(appointments, selectedID)
	if len(block) == 0 {
		return sm.NewResult(sm.StateListAppointments).
			WithText("Cita no encontrada. Selecciona otra.").
			WithClearCtx("selected_appointment_id"), nil
	}

	if procRepo == nil {
		r := backToAppointmentAction(sess, apptSvc)
		r.Messages = append([]sm.OutboundMessage{&sm.TextMessage{Text: "No se pudo consultar la preparación en este momento."}}, r.Messages...)
		return r, nil
	}

	var prepText string
	var address string
	seen := make(map[string]bool)
	for _, appt := range block {
		for _, proc := range appt.Procedures {
			if proc.CupCode == "" || seen[proc.CupCode] {
				continue
			}
			seen[proc.CupCode] = true
			p, err := procRepo.FindByCode(ctx, proc.CupCode)
			if err != nil || p == nil {
				continue
			}
			if p.Preparation != "" {
				prepText += fmt.Sprintf("\n\n*%s:*\n%s", proc.CupName, p.Preparation)
			}
			if p.VideoURL != "" {
				prepText += fmt.Sprintf("\n📹 Ver video: %s", p.VideoURL)
			}
			if p.AudioURL != "" {
				prepText += fmt.Sprintf("\n🎵 Audio: %s", p.AudioURL)
			}
			if p.Address != "" && address == "" {
				address = p.Address
			}
		}
	}

	var msg string
	if prepText == "" {
		msg = "No se encontraron instrucciones de preparación para esta cita."
	} else {
		msg = "📋 *Preparación para tu cita:*" + prepText
		if address != "" {
			if addrMapper != nil {
				msg += "\n\n" + addrMapper.FormatAddress(address)
			} else {
				msg += fmt.Sprintf("\n\n📍 *Dirección:* %s", address)
			}
		}
	}

	r := backToAppointmentAction(sess, apptSvc)
	r.Messages = append([]sm.OutboundMessage{&sm.TextMessage{Text: msg}}, r.Messages...)
	return r.WithEvent("appointment_preparation_viewed", map[string]interface{}{
		"appointment_id": selectedID,
	}), nil
}

// buildAppointmentDetail construye el texto de detalle de una cita seleccionada.
func buildAppointmentDetail(apptSvc *services.AppointmentService, appointments []domain.Appointment, selectedID string) string {
	var appt *domain.Appointment
	for i, a := range appointments {
		if a.ID == selectedID {
			appt = &appointments[i]
			break
		}
	}

	if appt == nil {
		return ""
	}

	block := apptSvc.FindConsecutiveBlock(appointments, selectedID)

	statusText := "Pendiente"
	if appt.Confirmed {
		statusText = "Confirmada"
	}

	cupName := services.GetFirstCupName(*appt)
	detail := fmt.Sprintf("*Detalle de tu cita:*\n\n"+
		"Procedimiento: %s\n"+
		"Doctor: %s\n"+
		"Fecha: %s\n"+
		"Hora: %s\n"+
		"Estado: %s",
		cupName,
		appt.DoctorName,
		utils.FormatFriendlyDate(appt.Date),
		services.FormatTimeSlot(appt.TimeSlot),
		statusText)

	if appt.Observations != "" {
		detail += fmt.Sprintf("\nObservaciones: %s", appt.Observations)
	}

	if len(block) > 1 {
		detail += fmt.Sprintf("\n\nEsta cita tiene *%d procedimientos consecutivos* que se gestionarán juntos.", len(block))
	}

	return detail
}

// appointmentActionRows retorna las filas de la lista de acciones para una cita.
func appointmentActionRows() []sm.ListRow {
	return []sm.ListRow{
		{ID: "appt_confirm", Title: "Confirmar cita", Description: "Confirmar asistencia a esta cita"},
		{ID: "appt_cancel", Title: "Cancelar cita", Description: "Cancelar esta cita"},
		{ID: "appt_reschedule", Title: "Reprogramar cita", Description: "Buscar nuevo horario para esta cita"},
		{ID: "appt_preparation", Title: "Ver preparación", Description: "Instrucciones de preparación para el examen"},
		{ID: "appt_back", Title: "Volver al listado", Description: "Ver otras citas programadas"},
		{ID: "appt_menu", Title: "Menú principal", Description: "Volver al menú principal"},
	}
}

// appointmentListData holds the data needed to build a WhatsApp list message
type appointmentListData struct {
	body    string
	button  string
	section sm.ListSection
}

// buildAppointmentList constructs the list display for appointments.
// Each appointment is shown as its own row (no block grouping for display).
// Block grouping is only used in confirm/cancel actions via FindConsecutiveBlock.
func buildAppointmentList(apptSvc *services.AppointmentService, appointments []domain.Appointment) appointmentListData {
	maxShow := 10

	rows := make([]sm.ListRow, 0, maxShow)
	for _, appt := range appointments {
		cupName := services.GetFirstCupName(appt)
		title := fmt.Sprintf("%s %s", utils.FormatFriendlyDateShort(appt.Date), services.FormatTimeSlot(appt.TimeSlot))
		desc := fmt.Sprintf("Dr. %s - %s", appt.DoctorName, cupName)

		rows = append(rows, sm.ListRow{
			ID:          appt.ID,
			Title:       truncate(title, 24),
			Description: truncate(desc, 72),
		})

		if len(rows) >= maxShow {
			break
		}
	}

	body := fmt.Sprintf("Tienes *%d cita(s)* programadas.", len(appointments))
	if len(appointments) > maxShow {
		body += fmt.Sprintf("\nMostrando las primeras %d:", maxShow)
	} else {
		body += "\nSelecciona una para ver detalles:"
	}

	return appointmentListData{
		body:    body,
		button:  "Ver citas",
		section: sm.ListSection{Title: "Tus citas", Rows: rows},
	}
}

// truncate corta un string a maxLen caracteres
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
