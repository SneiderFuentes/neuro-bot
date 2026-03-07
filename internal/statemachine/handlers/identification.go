package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/statemachine/validators"
)

// RegisterIdentificationHandlers registra ASK_DOCUMENT, PATIENT_LOOKUP, CONFIRM_IDENTITY
func RegisterIdentificationHandlers(m *sm.Machine, patientSvc *services.PatientService) {
	m.RegisterWithConfig(sm.StateAskDocument, sm.HandlerConfig{
		InputType:    sm.InputText,
		TextValidate: validators.Document,
		ErrorMsg:     "Por favor ingresa un número de documento válido (solo números, entre 5 y 15 dígitos).",
		Handler:      askDocumentHandler(),
	})
	m.Register(sm.StatePatientLookup, patientLookupHandler(patientSvc))
	m.Register(sm.StateConfirmIdentity, confirmIdentityHandler())
}

// ASK_DOCUMENT — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func askDocumentHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)
		return sm.NewResult(sm.StatePatientLookup).
			WithContext("patient_doc", input).
			WithEvent("document_entered", map[string]interface{}{"doc_length": len(input)}), nil
	}
}

// PATIENT_LOOKUP (automático) — busca paciente en BD externa
func patientLookupHandler(patientSvc *services.PatientService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		doc := sess.GetContext("patient_doc")

		patient, err := patientSvc.LookupByDocument(ctx, doc)
		if err != nil {
			return sm.NewResult(sm.StatePostActionMenu).
				WithText("Lo siento, hubo un problema al buscar tu información. Intenta más tarde.").
				WithButtons("¿Qué deseas hacer?",
					sm.Button{Text: "📅 Agendar otra cita", Payload: "otra_cita"},
					sm.Button{Text: "📋 Ver mis citas", Payload: "ver_citas"},
					sm.Button{Text: "🔄 Cambiar paciente", Payload: "cambiar_paciente"},
				).
				WithEvent("patient_lookup_error", map[string]interface{}{"error": err.Error()}), nil
		}

		if patient == nil {
			menuOption := sess.GetContext("menu_option")

			if menuOption == "agendar" {
				return sm.NewResult(sm.StateRegistrationStart).
					WithButtons("No encontramos un paciente con ese documento. Para agendar una cita necesitas registrarte primero.\n\n¿Deseas registrarte?",
						sm.Button{Text: "Sí, registrarme", Payload: "register_yes"},
						sm.Button{Text: "No, gracias", Payload: "register_no"},
					).
					WithEvent("patient_not_found", map[string]interface{}{"doc": doc, "can_register": true}), nil
			}

			// Menú consultar → no puede consultar sin estar registrado
			return sm.NewResult(sm.StatePostActionMenu).
				WithButtons("No encontramos un paciente con el documento *"+doc+"*. Verifica que el número sea correcto.\n\nSi eres paciente nuevo, selecciona la opción *Agendar cita* para registrarte.\n\n¿Qué deseas hacer?",
					sm.Button{Text: "📅 Agendar otra cita", Payload: "otra_cita"},
					sm.Button{Text: "📋 Ver mis citas", Payload: "ver_citas"},
					sm.Button{Text: "🔄 Cambiar paciente", Payload: "cambiar_paciente"},
				).
				WithEvent("patient_not_found", map[string]interface{}{"doc": doc, "can_register": false}), nil
		}

		// Paciente encontrado → guardar datos en sesión
		fullName := services.FormatFullName(patient)
		age := services.FormatAge(patient.BirthDate)

		return sm.NewResult(sm.StateConfirmIdentity).
			WithContext("patient_id", patient.ID).
			WithContext("patient_name", fullName).
			WithContext("patient_age", age).
			WithContext("patient_gender", patient.Gender).
			WithContext("patient_entity", patient.EntityCode).
			WithContext("patient_phone", patient.Phone).
			WithButtons(fmt.Sprintf("Encontré este paciente:\n\n*%s*\nDocumento: %s\n\n¿Eres tú?", fullName, doc),
				sm.Button{Text: "Sí, soy yo", Payload: "identity_yes"},
				sm.Button{Text: "No, no soy yo", Payload: "identity_no"},
			).
			WithEvent("patient_found", map[string]interface{}{
				"patient_id": patient.ID,
				"name":       fullName,
			}), nil
	}
}

// CONFIRM_IDENTITY (interactivo) — confirmar si es el paciente correcto
func confirmIdentityHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "identity_yes", "identity_no")
		if result != nil {
			return result, nil
		}

		switch selected {
		case "identity_yes":
			menuOption := sess.GetContext("menu_option")

			r := sm.NewResult("").
				WithEvent("patient_identified", map[string]interface{}{
					"patient_id": sess.GetContext("patient_id"),
				})

			// También guardar patient data en la sesión DB (campos directos)
			sess.PatientID = sess.GetContext("patient_id")
			sess.PatientDoc = sess.GetContext("patient_doc")
			sess.PatientName = sess.GetContext("patient_name")
			sess.PatientEntity = sess.GetContext("patient_entity")

			switch menuOption {
			case "consultar":
				r.NextState = sm.StateFetchAppointments
			case "agendar":
				// Bird V2: entity already selected before document entry → auto-update
				selectedEntity := sess.GetContext("selected_entity_code")
				if selectedEntity != "" {
					sess.PatientEntity = selectedEntity
					r.WithContext("patient_entity", selectedEntity)
				}
				r.NextState = sm.StateAskMedicalOrder
			default:
				r.NextState = sm.StatePostActionMenu
			}

			return r, nil

		case "identity_no":
			return sm.NewResult(sm.StateAskDocument).
				WithText("Entendido. Por favor ingresa tu número de documento correcto.").
				WithClearCtx("patient_id", "patient_name", "patient_age", "patient_gender", "patient_entity", "patient_phone").
				WithEvent("patient_identity_rejected", nil), nil
		}

		return nil, fmt.Errorf("unreachable")
	}
}
