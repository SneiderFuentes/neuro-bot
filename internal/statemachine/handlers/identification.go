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
	"github.com/neuro-bot/neuro-bot/internal/utils"
)

// RegisterIdentificationHandlers registra ASK_DOCUMENT, PATIENT_LOOKUP, CONFIRM_IDENTITY
// y los nuevos estados de verificación de datos de contacto.
func RegisterIdentificationHandlers(m *sm.Machine, patientSvc *services.PatientService) {
	m.RegisterWithConfig(sm.StateAskDocument, sm.HandlerConfig{
		InputType:    sm.InputText,
		TextValidate: validators.Document,
		ErrorMsg:     "Por favor ingresa un número de documento válido (solo números, entre 5 y 15 dígitos).",
		Handler:      askDocumentHandler(),
	})
	m.Register(sm.StatePatientLookup, patientLookupHandler(patientSvc))
	m.Register(sm.StateConfirmIdentity, confirmIdentityHandler())

	// Verificación de datos de contacto
	m.Register(sm.StateShowContactInfo, showContactInfoHandler())
	m.RegisterWithConfig(sm.StateConfirmContactInfo, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"contact_ok", "contact_update"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			phone := utils.FormatPhoneDisplay(sess.GetContext("patient_phone"))
			email := sess.GetContext("patient_email")
			if email == "" {
				email = "(no registrado)"
			}
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: fmt.Sprintf("Tus datos de contacto:\n\nCelular: %s\nEmail: %s\n\n¿Son correctos?", phone, email),
				Buttons: []sm.Button{
					{Text: "Sí, son correctos", Payload: "contact_ok"},
					{Text: "No, actualizar", Payload: "contact_update"},
				},
			})
		},
		Handler: confirmContactInfoHandler(patientSvc),
	})
	m.RegisterWithConfig(sm.StateAskUpdatePhone, sm.HandlerConfig{
		InputType:    sm.InputText,
		TextValidate: validators.ColombianPhone,
		ErrorMsg:     "Número no válido. Ingresa un celular colombiano de 10 dígitos (ej: 3001234567). Debe ser un número con WhatsApp.",
		Handler:      askUpdatePhoneHandler(),
	})
	m.Register(sm.StateAskUpdateEmail, askUpdateEmailHandler())
	m.Register(sm.StateUpdateContactInfo, updateContactInfoHandler(patientSvc))
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
			WithContext("patient_email", patient.Email).
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
			r := sm.NewResult(sm.StateShowContactInfo).
				WithEvent("patient_identified", map[string]interface{}{
					"patient_id": sess.GetContext("patient_id"),
				})

			// Guardar patient data en la sesión DB (campos directos)
			sess.PatientID = sess.GetContext("patient_id")
			sess.PatientDoc = sess.GetContext("patient_doc")
			sess.PatientName = sess.GetContext("patient_name")
			sess.PatientEntity = sess.GetContext("patient_entity")

			return r, nil

		case "identity_no":
			return sm.NewResult(sm.StateAskDocument).
				WithText("Entendido. Por favor ingresa tu número de documento correcto.").
				WithClearCtx("patient_id", "patient_name", "patient_age", "patient_gender", "patient_entity", "patient_phone", "patient_email").
				WithEvent("patient_identity_rejected", nil), nil
		}

		return nil, fmt.Errorf("unreachable")
	}
}

// routeAfterContactInfo determina el siguiente estado según menu_option.
// Centraliza la lógica de routing que antes estaba en confirmIdentityHandler.
// Persiste la entidad seleccionada en BD externa (equivalente a GetPatientByDocumentJob en Laravel).
func routeAfterContactInfo(ctx context.Context, sess *session.Session, r *sm.StateResult, patientSvc *services.PatientService) {
	menuOption := sess.GetContext("menu_option")
	switch menuOption {
	case "consultar":
		r.NextState = sm.StateFetchAppointments
	case "agendar":
		// Bird V2: entity already selected before document entry → auto-update
		selectedEntity := sess.GetContext("selected_entity_code")
		if selectedEntity != "" {
			sess.PatientEntity = selectedEntity
			r.WithContext("patient_entity", selectedEntity)
			// Persistir entidad en BD externa
			patientID := sess.GetContext("patient_id")
			if patientSvc != nil && patientID != "" {
				_ = patientSvc.UpdateEntity(ctx, patientID, selectedEntity)
			}
		}
		r.NextState = sm.StateAskMedicalOrder
		r.Messages = append(r.Messages, &sm.ButtonMessage{
			Text: "¿Tienes tu orden médica a la mano?\n\nPuedes enviar una *foto* de la orden para que la lea automáticamente, o puedes ingresar el procedimiento *manualmente*.",
			Buttons: []sm.Button{
				{Text: "Enviar foto", Payload: "order_photo"},
				{Text: "Ingresar manual", Payload: "order_manual"},
			},
		})
	default:
		r.NextState = sm.StatePostActionMenu
	}
}

// SHOW_CONTACT_INFO (automático) — evalúa datos de contacto y decide siguiente paso
func showContactInfoHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		rawPhone := sess.GetContext("patient_phone")
		rawEmail := sess.GetContext("patient_email")

		parsedPhone := utils.ParseColombianPhone(rawPhone)
		hasValidPhone := parsedPhone != ""
		hasEmail := rawEmail != "" && strings.ToLower(rawEmail) != "null"

		// Si el celular no es válido → pedir obligatoriamente
		if !hasValidPhone {
			return sm.NewResult(sm.StateAskUpdatePhone).
				WithText("No tenemos un celular válido registrado para ti.\n\nNecesitamos tu número de celular con WhatsApp para enviarte recordatorios y confirmaciones de tus citas.\n\nIngresa tu número de celular (ej: 3001234567):").
				WithEvent("contact_info_missing", map[string]interface{}{"missing": "phone"}), nil
		}

		// Si tiene celular pero no email → pedir email
		if !hasEmail {
			return sm.NewResult(sm.StateAskUpdateEmail).
				WithContext("contact_new_phone", parsedPhone).
				WithText(fmt.Sprintf("Tu celular registrado es: *%s*\n\nAhora necesitamos tu correo electrónico para enviarte información importante.\n\nIngresa tu email o responde *NA* si no tienes:", utils.FormatPhoneDisplay(rawPhone))).
				WithEvent("contact_info_missing", map[string]interface{}{"missing": "email"}), nil
		}

		// Ambos datos completos → mostrar y pedir confirmación
		phoneDisplay := utils.FormatPhoneDisplay(rawPhone)

		return sm.NewResult(sm.StateConfirmContactInfo).
			WithButtons(fmt.Sprintf("Tus datos de contacto registrados son:\n\nCelular: *%s*\nEmail: *%s*\n\nEs importante que estén actualizados ya que los usamos para enviarte recordatorios y confirmaciones de citas por WhatsApp.\n\n¿Son correctos?", phoneDisplay, rawEmail),
				sm.Button{Text: "Sí, son correctos", Payload: "contact_ok"},
				sm.Button{Text: "No, actualizar", Payload: "contact_update"},
			).
			WithEvent("contact_info_shown", map[string]interface{}{
				"phone": phoneDisplay,
				"email": rawEmail,
			}), nil
	}
}

// CONFIRM_CONTACT_INFO (interactivo) — confirmar o actualizar datos de contacto
func confirmContactInfoHandler(patientSvc *services.PatientService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "contact_ok", "contact_update")
		if result != nil {
			return result, nil
		}

		switch selected {
		case "contact_ok":
			r := sm.NewResult("").
				WithEvent("contact_info_confirmed", nil)
			routeAfterContactInfo(ctx, sess, r, patientSvc)
			return r, nil

		case "contact_update":
			return sm.NewResult(sm.StateAskUpdatePhone).
				WithText("Ingresa tu número de celular (debe ser un número con WhatsApp):\n\nEj: 3001234567").
				WithEvent("contact_update_requested", nil), nil
		}

		return nil, fmt.Errorf("unreachable")
	}
}

// ASK_UPDATE_PHONE (interactivo) — capturar nuevo celular
func askUpdatePhoneHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)
		parsed := utils.ParseColombianPhone(input)

		return sm.NewResult(sm.StateAskUpdateEmail).
			WithContext("contact_new_phone", parsed).
			WithText("Ingresa tu correo electrónico o responde *NA* si no tienes:").
			WithEvent("contact_phone_updated", map[string]interface{}{"phone": parsed}), nil
	}
}

// ASK_UPDATE_EMAIL (interactivo) — capturar nuevo email
func askUpdateEmailHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)
		lower := strings.ToLower(input)

		// Aceptar NA y variantes
		naResponses := map[string]bool{
			"no tengo": true, "no": true, "na": true, "n/a": true, "-": true, "ninguno": true,
		}
		if naResponses[lower] {
			return sm.NewResult(sm.StateUpdateContactInfo).
				WithContext("contact_new_email", "").
				WithEvent("contact_email_skipped", nil), nil
		}

		// Validar formato de email
		retryResult := sm.ValidateWithRetry(sess, lower, validators.Email, "Email no válido. Ingresa un correo como ejemplo@correo.com o responde *NA* si no tienes.")
		if retryResult != nil {
			return retryResult, nil
		}

		sess.RetryCount = 0
		return sm.NewResult(sm.StateUpdateContactInfo).
			WithContext("contact_new_email", lower).
			WithEvent("contact_email_updated", map[string]interface{}{"email": lower}), nil
	}
}

// UPDATE_CONTACT_INFO (automático) — actualizar datos en BD externa y continuar flujo
func updateContactInfoHandler(patientSvc *services.PatientService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		patientID := sess.GetContext("patient_id")
		newPhone := sess.GetContext("contact_new_phone")
		newEmail := sess.GetContext("contact_new_email")

		err := patientSvc.UpdateContactInfo(ctx, patientID, newPhone, newEmail)
		if err != nil {
			// No bloquear el flujo por error de actualización
			r := sm.NewResult("").
				WithText("No pudimos actualizar tus datos en este momento, pero puedes continuar.").
				WithEvent("contact_update_error", map[string]interface{}{"error": err.Error()})
			routeAfterContactInfo(ctx, sess, r, patientSvc)
			return r, nil
		}

		// Actualizar contexto de sesión con los nuevos valores
		r := sm.NewResult("").
			WithContext("patient_phone", newPhone).
			WithContext("patient_email", newEmail).
			WithText("Datos de contacto actualizados correctamente.").
			WithEvent("contact_info_updated", map[string]interface{}{
				"phone": newPhone,
				"email": newEmail,
			})
		routeAfterContactInfo(ctx, sess, r, patientSvc)
		return r, nil
	}
}
