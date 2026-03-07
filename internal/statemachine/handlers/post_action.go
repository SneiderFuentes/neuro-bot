package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
)

// RegisterPostActionHandlers registra los handlers de post-acción y estados terminales (Fase 11).
func RegisterPostActionHandlers(m *sm.Machine) {
	m.Register(sm.StatePostActionMenu, postActionMenuHandler())
	m.Register(sm.StateFallbackMenu, fallbackMenuHandler())
	m.Register(sm.StateChangePatient, changePatientHandler())
	m.Register(sm.StateFarewell, farewellHandler())
	m.Register(sm.StateTerminated, terminatedHandler())
}

// POST_ACTION_MENU (interactivo) — menú con 3 botones + texto "agente".
// Acepta payloads: otra_cita, ver_citas, cambiar_paciente, y texto "agente".
func postActionMenuHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		payload := msg.Text
		if msg.PostbackPayload != "" {
			payload = msg.PostbackPayload
		}

		// Accept "agente" as free-text input (case insensitive)
		if strings.EqualFold(strings.TrimSpace(payload), "agente") {
			return sm.NewResult(sm.StateEscalateToAgent).
				WithEvent("post_action_selected", map[string]interface{}{"action": "escalate"}), nil
		}

		result, selected := sm.ValidateButtonResponse(sess, msg,
			"otra_cita", "ver_citas", "cambiar_paciente")
		if result != nil {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: "¿Qué deseas hacer ahora?",
				Buttons: []sm.Button{
					{Text: "Agendar otra cita", Payload: "otra_cita"},
					{Text: "Consultar mis citas", Payload: "ver_citas"},
					{Text: "Cambiar paciente", Payload: "cambiar_paciente"},
				},
			})
			result.Messages = append(result.Messages, &sm.TextMessage{
				Text: "También puedes escribir *agente* para hablar con un asesor.",
			})
			return result, nil
		}

		switch selected {
		case "otra_cita":
			// Limpiar datos de booking pero mantener paciente
			bookingKeys := []string{
				"cups_code", "cups_name", "is_contrasted", "is_sedated", "espacios",
				"procedures_json", "total_procedures", "current_procedure_idx",
				"gfr_creatinine", "gfr_height_cm", "gfr_weight_kg",
				"gfr_disease_type", "gfr_calculated",
				"is_pregnant", "baby_weight_cat", "preferred_doctor_doc",
				"selected_slot_id", "available_slots_json", "slots_after_date",
				"ocr_cups_json", "created_appointment_id",
				"booking_failure_reason", "appointments_json", "selected_appointment_id",
				"cups_preparation", "cups_video_url", "cups_audio_url",
				"client_type",
			}

			return sm.NewResult(sm.StateAskMedicalOrder).
				WithClearCtx(bookingKeys...).
				WithEvent("post_action_selected", map[string]interface{}{"action": "schedule_another"}), nil

		case "ver_citas":
			return sm.NewResult(sm.StateFetchAppointments).
				WithEvent("post_action_selected", map[string]interface{}{"action": "consult_appointments"}), nil

		case "cambiar_paciente":
			// Limpiar TODO el contexto
			return sm.NewResult(sm.StateAskDocument).
				WithClearCtx("__all__").
				WithEvent("post_action_selected", map[string]interface{}{"action": "change_patient"}), nil
		}

		return nil, fmt.Errorf("unreachable")
	}
}

// FALLBACK_MENU (interactivo) — menú de reinicio/cierre cuando se superan los reintentos máximos.
// No usa ValidateButtonResponse para evitar recursión infinita de reintentos.
func fallbackMenuHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		payload := msg.PostbackPayload
		if payload == "" {
			payload = strings.TrimSpace(msg.Text)
		}

		switch payload {
		case "action:restart", "1":
			sess.RetryCount = 0
			return sm.NewResult(sm.StateGreeting).
				WithClearCtx("__all__").
				WithEvent("fallback_restart", nil), nil
		case "action:end", "2":
			return sm.NewResult(sm.StateFarewell).
				WithEvent("fallback_end", nil), nil
		}

		// Invalid input → re-show buttons (sin incrementar reintentos)
		return sm.NewResult(sess.CurrentState).
			WithButtons("Por favor selecciona una opción:",
				sm.Button{Text: "Volver al inicio", Payload: "action:restart"},
				sm.Button{Text: "Terminar chat", Payload: "action:end"},
			), nil
	}
}

// CHANGE_PATIENT (automático) — solicita nuevo documento.
func changePatientHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		return sm.NewResult(sm.StateAskDocument).
			WithText("Ingresa el número de documento del nuevo paciente:").
			WithClearCtx("__all__").
			WithEvent("patient_changed", nil), nil
	}
}

// FAREWELL (automático) — despedida.
func farewellHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		return sm.NewResult(sm.StateTerminated).
			WithText("Gracias por usar nuestro servicio de agendamiento. ¡Hasta pronto!").
			WithEvent("farewell_sent", nil), nil
	}
}

// TERMINATED (automático) — marca sesión como completada.
// Cualquier nuevo mensaje creará una sesión nueva.
func terminatedHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		sess.Status = session.StatusCompleted
		return sm.NewResult(sm.StateTerminated).
			WithEvent("session_completed", nil), nil
	}
}
