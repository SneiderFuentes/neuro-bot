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
	m.Register(sm.StateFarewell, farewellHandler())
	m.Register(sm.StateTerminated, terminatedHandler())
}

// buildPostActionList retorna la lista estándar de opciones post-acción.
func buildPostActionList(body string) *sm.ListMessage {
	return &sm.ListMessage{
		Body:  body,
		Title: "Ver opciones",
		Sections: []sm.ListSection{{
			Title: "Opciones",
			Rows: []sm.ListRow{
				{ID: "ver_citas", Title: "Consultar mis citas", Description: "Ver citas programadas"},
				{ID: "menu_principal", Title: "Menú principal", Description: "Volver al menú principal"},
				{ID: "terminar_chat", Title: "Terminar chat", Description: "Finalizar la conversación"},
			},
		}},
	}
}

// POST_ACTION_MENU (interactivo) — menú con lista de 3 opciones + texto "agente".
// Acepta payloads: ver_citas, menu_principal, terminar_chat, y texto "agente".
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
			"ver_citas", "menu_principal", "terminar_chat")
		if result != nil {
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}
			result.Messages = nil
			result.Messages = append(result.Messages, buildPostActionList("¿Qué deseas hacer ahora?\n\nTambién puedes escribir *agente* para hablar con un asesor."))
			return result, nil
		}

		switch selected {
		case "ver_citas":
			return sm.NewResult(sm.StateFetchAppointments).
				WithEvent("post_action_selected", map[string]interface{}{"action": "consult_appointments"}), nil

		case "menu_principal":
			return sm.NewResult(sm.StateGreeting).
				WithEvent("post_action_selected", map[string]interface{}{"action": "main_menu"}), nil

		case "terminar_chat":
			return sm.NewResult(sm.StateFarewell).
				WithEvent("post_action_selected", map[string]interface{}{"action": "end_chat"}), nil
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
