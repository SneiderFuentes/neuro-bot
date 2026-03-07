package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/utils"
)

// CancellationCallback is called after a patient cancels an appointment via the bot.
// cupsCode is the CUPS code of the freed slot (called once per unique CUPS in the block).
type CancellationCallback func(ctx context.Context, cupsCode string)

// RegisterAppointmentHandlers registra FETCH_APPOINTMENTS, LIST_APPOINTMENTS, APPOINTMENT_ACTION
func RegisterAppointmentHandlers(m *sm.Machine, apptSvc *services.AppointmentService, onCancel CancellationCallback) {
	m.Register(sm.StateFetchAppointments, fetchAppointmentsHandler(apptSvc))
	m.Register(sm.StateListAppointments, listAppointmentsHandler(apptSvc))
	m.Register(sm.StateAppointmentAction, appointmentActionHandler(apptSvc, onCancel))
}

// FETCH_APPOINTMENTS (automático) — consulta citas del paciente
func fetchAppointmentsHandler(apptSvc *services.AppointmentService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		patientID := sess.GetContext("patient_id")

		appointments, err := apptSvc.GetUpcomingAppointments(ctx, patientID)
		if err != nil {
			return sm.NewResult(sm.StatePostActionMenu).
				WithText("Error al consultar tus citas. Intenta más tarde.").
				WithEvent("fetch_appointments_error", map[string]interface{}{"error": err.Error()}), nil
		}

		if len(appointments) == 0 {
			return sm.NewResult(sm.StatePostActionMenu).
				WithText("No tienes citas pendientes o confirmadas.").
				WithEvent("no_appointments_found", nil), nil
		}

		// Serializar citas en contexto para los siguientes estados
		apptJSON, _ := json.Marshal(appointments)

		return sm.NewResult(sm.StateListAppointments).
			WithContext("appointments_json", string(apptJSON)).
			WithEvent("appointments_found", map[string]interface{}{"count": len(appointments)}), nil
	}
}

// LIST_APPOINTMENTS (interactivo, lista) — muestra citas como lista interactiva
func listAppointmentsHandler(apptSvc *services.AppointmentService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		// Si es postback con ID de cita seleccionada
		if msg.IsPostback {
			var appts []domain.Appointment
			if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appts); err == nil {
				for _, a := range appts {
					if a.ID == msg.PostbackPayload {
						sess.RetryCount = 0
						return sm.NewResult(sm.StateAppointmentAction).
							WithContext("selected_appointment_id", msg.PostbackPayload).
							WithEvent("appointment_selected", map[string]interface{}{"id": msg.PostbackPayload}), nil
					}
				}
			}
			// Invalid postback ID — fall through to retry + re-show list
		}

		// Texto o postback inválido — retry antes de re-mostrar lista
		result := sm.RetryOrEscalate(sess, "Selecciona una cita de la lista.")
		if result.NextState == sm.StateEscalateToAgent {
			return result, nil
		}

		// Cargar citas del contexto
		var appointments []domain.Appointment
		if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments); err != nil {
			return sm.NewResult(sm.StatePostActionMenu).
				WithText("Error al cargar las citas. Intenta de nuevo."), nil
		}

		// Construir lista (máx 10 items para WhatsApp)
		maxShow := 10
		if len(appointments) < maxShow {
			maxShow = len(appointments)
		}

		// Agrupar: detectar citas que son parte del mismo bloque consecutivo
		// para mostrar una sola fila por bloque
		type displayItem struct {
			ID          string
			DateDisplay string
			TimeDisplay string
			DoctorName  string
			CupName     string
			BlockSize   int
		}

		shown := make(map[string]bool) // IDs ya incluidas en un bloque
		var items []displayItem

		for _, appt := range appointments {
			if shown[appt.ID] {
				continue
			}

			block := apptSvc.FindConsecutiveBlock(appointments, appt.ID)
			for _, b := range block {
				shown[b.ID] = true
			}

			cupName := services.GetFirstCupName(appt)
			if len(block) > 1 {
				// Recolectar todos los nombres de procedimientos del bloque
				cupNames := make(map[string]bool)
				for _, b := range block {
					name := services.GetFirstCupName(b)
					cupNames[name] = true
				}
				if len(cupNames) > 1 {
					cupName = fmt.Sprintf("%s (+%d proc.)", cupName, len(cupNames)-1)
				}
			}

			items = append(items, displayItem{
				ID:          appt.ID, // Usamos el ID de la primera cita del bloque
				DateDisplay: utils.FormatFriendlyDateShort(appt.Date),
				TimeDisplay: services.FormatTimeSlot(appt.TimeSlot),
				DoctorName:  appt.DoctorName,
				CupName:     cupName,
				BlockSize:   len(block),
			})

			if len(items) >= maxShow {
				break
			}
		}

		rows := make([]sm.ListRow, len(items))
		for i, item := range items {
			title := fmt.Sprintf("%s %s", item.DateDisplay, item.TimeDisplay)
			desc := fmt.Sprintf("Dr. %s - %s", item.DoctorName, item.CupName)
			if item.BlockSize > 1 {
				desc += fmt.Sprintf(" (%d citas)", item.BlockSize)
			}
			rows[i] = sm.ListRow{
				ID:          item.ID,
				Title:       truncate(title, 24),
				Description: truncate(desc, 72),
			}
		}

		body := fmt.Sprintf("Tienes *%d cita(s)* pendientes.\nSelecciona una para ver detalles:", len(appointments))

		return sm.NewResult(sess.CurrentState).
			WithList(body, "Ver citas", sm.ListSection{Title: "Tus citas", Rows: rows}).
			WithEvent("appointments_listed", map[string]interface{}{"shown": len(items)}), nil
	}
}

// APPOINTMENT_ACTION (interactivo, botones) — detalle de cita + confirmar/cancelar/volver
func appointmentActionHandler(apptSvc *services.AppointmentService, onCancel CancellationCallback) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selectedID := sess.GetContext("selected_appointment_id")

		result, selected := sm.ValidateButtonResponse(sess, msg, "appt_confirm", "appt_cancel", "appt_back")
		if result != nil {
			// Primera entrada o input inválido: mostrar detalle de la cita
			var appointments []domain.Appointment
			if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments); err != nil {
				return sm.NewResult(sm.StatePostActionMenu).
					WithText("Error al cargar las citas."), nil
			}

			var appt *domain.Appointment
			for i, a := range appointments {
				if a.ID == selectedID {
					appt = &appointments[i]
					break
				}
			}

			if appt == nil {
				return sm.NewResult(sm.StateListAppointments).
					WithText("Cita no encontrada. Selecciona otra.").
					WithClearCtx("selected_appointment_id"), nil
			}

			// Detectar bloque consecutivo
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

			return sm.NewResult(sess.CurrentState).
				WithButtons(detail,
					sm.Button{Text: "Confirmar", Payload: "appt_confirm"},
					sm.Button{Text: "Cancelar cita", Payload: "appt_cancel"},
					sm.Button{Text: "Volver", Payload: "appt_back"},
				), nil
		}

		// Procesar acción seleccionada
		switch selected {
		case "appt_confirm":
			return handleConfirmAppointment(ctx, sess, apptSvc, selectedID)
		case "appt_cancel":
			return handleCancelAppointment(ctx, sess, apptSvc, selectedID, onCancel)
		case "appt_back":
			return sm.NewResult(sm.StateListAppointments).
				WithClearCtx("selected_appointment_id"), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

func handleConfirmAppointment(ctx context.Context, sess *session.Session, apptSvc *services.AppointmentService, selectedID string) (*sm.StateResult, error) {
	var appointments []domain.Appointment
	if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments); err != nil {
		return sm.NewResult(sm.StatePostActionMenu).
			WithText("Error al procesar la cita."), nil
	}

	block := apptSvc.FindConsecutiveBlock(appointments, selectedID)
	if len(block) == 0 {
		return sm.NewResult(sm.StateListAppointments).
			WithText("Cita no encontrada.").
			WithClearCtx("selected_appointment_id"), nil
	}

	if err := apptSvc.ConfirmBlock(ctx, block, "whatsapp", ""); err != nil {
		return sm.NewResult(sm.StatePostActionMenu).
			WithText("Error al confirmar la cita. Intenta más tarde.").
			WithEvent("appointment_confirm_error", map[string]interface{}{"error": err.Error()}), nil
	}

	msg := fmt.Sprintf("*Cita confirmada exitosamente!*\n\n%d cita(s) confirmada(s).", len(block))

	return sm.NewResult(sm.StatePostActionMenu).
		WithText(msg).
		WithClearCtx("selected_appointment_id", "appointments_json").
		WithEvent("appointment_confirmed", map[string]interface{}{
			"appointment_id": selectedID,
			"block_size":     len(block),
		}), nil
}

func handleCancelAppointment(ctx context.Context, sess *session.Session, apptSvc *services.AppointmentService, selectedID string, onCancel CancellationCallback) (*sm.StateResult, error) {
	var appointments []domain.Appointment
	if err := json.Unmarshal([]byte(sess.GetContext("appointments_json")), &appointments); err != nil {
		return sm.NewResult(sm.StatePostActionMenu).
			WithText("Error al procesar la cita."), nil
	}

	block := apptSvc.FindConsecutiveBlock(appointments, selectedID)
	if len(block) == 0 {
		return sm.NewResult(sm.StateListAppointments).
			WithText("Cita no encontrada.").
			WithClearCtx("selected_appointment_id"), nil
	}

	if err := apptSvc.CancelBlock(ctx, block, "Cancelada por paciente vía WhatsApp", "whatsapp", ""); err != nil {
		return sm.NewResult(sm.StatePostActionMenu).
			WithText("Error al cancelar la cita. Intenta más tarde.").
			WithEvent("appointment_cancel_error", map[string]interface{}{"error": err.Error()}), nil
	}

	// Cambio 13: Notify waiting list for freed CUPS codes
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

	return sm.NewResult(sm.StatePostActionMenu).
		WithText(msg).
		WithClearCtx("selected_appointment_id", "appointments_json").
		WithEvent("appointment_cancelled", map[string]interface{}{
			"appointment_id": selectedID,
			"block_size":     len(block),
		}), nil
}

// truncate corta un string a maxLen caracteres
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
