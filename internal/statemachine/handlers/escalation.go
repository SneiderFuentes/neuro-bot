package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/config"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
)

// RegisterEscalationHandlers registra los handlers de escalación a agente (Fase 11).
func RegisterEscalationHandlers(m *sm.Machine, birdClient *bird.Client, cfg *config.Config) {
	m.Register(sm.StateEscalateToAgent, escalateHandler(birdClient, cfg))
	m.Register(sm.StateEscalated, escalatedHandler())
}

// ESCALATE_TO_AGENT (automático) — transfiere la conversación a un agente humano.
// Routes to the correct Bird team based on the CUPS procedure code.
func escalateHandler(birdClient *bird.Client, cfg *config.Config) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		// 1. Determine team based on service (CUPS code)
		cupsCode := sess.GetContext("cups_code")
		teamID := cfg.ResolveTeamForCups(cupsCode)

		// 2. Read actual pre-escalation state (set by machine.go auto-chain)
		preState := sess.GetContext("_pre_auto_state")
		if preState == "" || preState == sm.StateEscalateToAgent {
			preState = sess.CurrentState
		}
		sess.SetContext("pre_escalation_state", preState)

		slog.Debug("escalation_start",
			"session_id", sess.ID,
			"phone", msg.Phone,
			"cups_code", cupsCode,
			"team_id", teamID,
			"state", preState,
		)
		sess.SetContext("escalation_team", teamID)

		// 3. Resolve conversationID: msg → session → cache (from conversation.created webhook)
		conversationID := msg.ConversationID
		if conversationID == "" {
			conversationID = sess.ConversationID
		}
		if conversationID == "" {
			conversationID = birdClient.GetCachedConversationID(msg.Phone)
			if conversationID != "" {
				sess.ConversationID = conversationID
			}
		}

		// Resolve team name for display
		teamName := resolveTeamName(teamID, cfg)

		// 4. Try to escalate — only notify patient on SUCCESS
		if err := birdClient.EscalateToAgent(conversationID, msg.Phone, teamID, teamName, sess.PatientName, cfg.BirdTeamFallback); err != nil {
			slog.Error("escalation failed",
				"error", err,
				"phone", msg.Phone,
				"team_id", teamID,
				"conversation_id", conversationID,
				"session_id", sess.ID,
			)
			// Agent unavailable → silent fallback to restart/end menu (no "connecting" message shown)
			return sm.NewResult(sm.StateFallbackMenu).
				WithButtons("¿Qué deseas hacer?",
					sm.Button{Text: "Volver al inicio", Payload: "action:restart"},
					sm.Button{Text: "Terminar chat", Payload: "action:end"},
				).
				WithEvent("escalation_failed", map[string]interface{}{"error": err.Error()}), nil
		}

		// 4b. Persist conversationID in session (may have been resolved by API lookup inside EscalateToAgent)
		if sess.ConversationID == "" {
			if cached := birdClient.GetCachedConversationID(msg.Phone); cached != "" {
				sess.ConversationID = cached
			}
		}

		// 5. Escalation succeeded — notify patient (visible in WhatsApp + Inbox)
		birdClient.SendText(msg.Phone, conversationID, "Te voy a conectar con un agente. Un momento por favor...")

		// 6. Send detailed context summary for the agent (Inbox only — invisible to patient)
		summary := buildAgentSummary(sess, cupsCode, teamName)
		birdClient.SendInternalText(conversationID, summary)

		// 7. Send contextual commands for the agent (Inbox only — invisible to patient)
		commands := buildAgentCommands(sess, cupsCode)
		birdClient.SendInternalText(conversationID, commands)

		// 8. Mark session as escalated (in-memory, persisted by worker pool)
		sess.Status = session.StatusEscalated

		return sm.NewResult(sm.StateEscalated).
			WithContext("pre_escalation_state", preState).
			WithContext("escalation_team", teamID).
			WithEvent("escalated_to_agent", map[string]interface{}{
				"from_state": preState,
				"team_id":    teamID,
				"cups_code":  cupsCode,
				"patient_id": sess.GetContext("patient_id"),
			}), nil
	}
}

// ESCALATED — estado terminal especial.
// Mientras la sesión esté escalada, NO se procesan más mensajes del bot.
// El worker pool filtra mensajes de sesiones escaladas antes de llegar aquí.
func escalatedHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		// No hacer nada — el agente maneja la conversación
		return sm.NewResult(sm.StateEscalated), nil
	}
}

// resolveTeamName returns a human-readable team name from the team ID.
func resolveTeamName(teamID string, cfg *config.Config) string {
	switch teamID {
	case cfg.BirdTeamGrupoA:
		return "Grupo A (Imagenes)"
	case cfg.BirdTeamGrupoB:
		return "Grupo B (Neuro/Fisiatria)"
	default:
		return "Call Center"
	}
}

// buildAgentSummary generates a formatted context summary for the human agent.
func buildAgentSummary(sess *session.Session, cupsCode, teamName string) string {
	patientName := sess.GetContext("patient_name")
	if patientName == "" {
		patientName = sess.PatientName
	}
	patientDoc := sess.GetContext("patient_doc")
	if patientDoc == "" {
		patientDoc = sess.PatientDoc
	}
	menuOption := sess.GetContext("menu_option")
	if menuOption == "" {
		menuOption = sess.MenuOption
	}
	cupsName := sess.GetContext("cups_name")
	serviceName := sess.GetContext("service_name")

	prevState := sess.GetContext("pre_escalation_state")
	if prevState == "" {
		prevState = sess.CurrentState
	}

	summary := fmt.Sprintf("Transferencia de chatbot\n\n"+
		"Paciente: %s\n"+
		"Documento: %s\n",
		patientName, patientDoc)

	if serviceName != "" {
		summary += fmt.Sprintf("Servicio: %s\n", serviceName)
	}
	if cupsCode != "" {
		summary += fmt.Sprintf("Procedimiento: %s (%s)\n", cupsName, cupsCode)
	}
	summary += fmt.Sprintf("Estado anterior: %s\n"+
		"Menu: %s\n"+
		"Equipo: %s",
		prevState, menuOption, teamName)

	return summary
}

// regFieldLabels maps registration states to human-readable field descriptions for agent commands.
var regFieldLabels = map[string]string{
	sm.StateRegDocumentType:    "tipo de documento (CC, TI, CE, PA, RC, MS, AS)",
	sm.StateRegFirstSurname:    "primer apellido",
	sm.StateRegSecondSurname:   "segundo apellido (o NA si no tiene)",
	sm.StateRegFirstName:       "primer nombre",
	sm.StateRegSecondName:      "segundo nombre (o NA si no tiene)",
	sm.StateRegBirthDate:       "fecha de nacimiento (formato: AAAA-MM-DD)",
	sm.StateRegBirthPlace:      "ciudad de nacimiento",
	sm.StateRegGender:          "genero (M o F)",
	sm.StateRegMaritalStatus:   "estado civil (SOLTERO, CASADO, UNION LIBRE, DIVORCIADO, VIUDO)",
	sm.StateRegAddress:         "direccion completa",
	sm.StateRegPhone:           "telefono (formato colombiano, ej: 3001234567)",
	sm.StateRegPhone2:          "telefono secundario (o NA)",
	sm.StateRegEmail:           "correo electronico (o NA)",
	sm.StateRegOccupation:      "ocupacion",
	sm.StateRegMunicipality:    "municipio de residencia",
	sm.StateRegClientType:      "tipo de cliente (PARTICULAR, EPS, SOAT)",
	sm.StateRegUserType:        "tipo de usuario (CONTRIBUTIVO, SUBSIDIADO, PARTICULAR)",
	sm.StateRegAffiliationType: "tipo de afiliacion (COTIZANTE, BENEFICIARIO, OTRO)",
	sm.StateRegEntity:          "nombre de la entidad/EPS",
	sm.StateRegZone:            "zona (U=Urbana, R=Rural)",
}

// buildAgentCommands generates contextual instructions for the agent based on escalation state.
func buildAgentCommands(sess *session.Session, cupsCode string) string {
	preState := sess.GetContext("pre_escalation_state")
	if preState == "" {
		preState = sess.CurrentState
	}

	patientName := sess.GetContext("patient_name")
	if patientName == "" {
		patientName = sess.PatientName
	}
	patientDoc := sess.GetContext("patient_doc")
	if patientDoc == "" {
		patientDoc = sess.PatientDoc
	}
	cupsName := sess.GetContext("cups_name")
	menuOption := sess.GetContext("menu_option")

	var situation, actions string

	switch preState {

	// --- Menu Principal ---
	case sm.StateMainMenu:
		situation = "El paciente no pudo seleccionar una opcion del menu principal."
		actions = "- Preguntale que necesita y usa el comando correspondiente:\n" +
			"  /bot resume MAIN_MENU — Mostrar menu de nuevo\n" +
			"  /bot resume ASK_DOCUMENT — Si quiere agendar o consultar citas"

	case sm.StateOutOfHoursMenu:
		situation = "El paciente intento usar el bot fuera de horario."
		actions = "- Informale el horario de atencion:\n" +
			"  /bot resume OUT_OF_HOURS_MENU — Mostrar opciones fuera de horario\n" +
			"  /bot cerrar"

	// --- Identificacion ---
	case sm.StateAskDocument:
		situation = fmt.Sprintf("El paciente no logro ingresar su numero de documento.\nMenu: %s", menuOption)
		actions = "- Preguntale su numero de documento y envialo:\n" +
			"  /bot resume ASK_DOCUMENT 1234567890\n" +
			"  (reemplaza con el documento real del paciente)"

	case sm.StateConfirmIdentity:
		situation = fmt.Sprintf("El paciente no pudo confirmar su identidad.\nPaciente: %s | Doc: %s", patientName, patientDoc)
		actions = "- Verificale los datos y luego:\n" +
			"  /bot resume CONFIRM_IDENTITY — Mostrar confirmacion de nuevo\n" +
			"  /bot resume ASK_DOCUMENT — Reingresar documento"

	// --- Entity Management ---
	case sm.StateAskClientType:
		situation = fmt.Sprintf("El paciente no pudo seleccionar su tipo de entidad (EPS, PARTICULAR, etc.).\nPaciente: %s | Doc: %s", patientName, patientDoc)
		actions = "- Preguntale su tipo de entidad:\n" +
			"  /bot resume ASK_CLIENT_TYPE — Mostrar tipos de entidad de nuevo\n" +
			"  /bot resume ASK_DOCUMENT — Si necesita reingresar documento"

	case sm.StateAskEntityNumber:
		clientType := sess.GetContext("client_type")
		category := sess.GetContext("entity_category")
		situation = fmt.Sprintf("El paciente no pudo seleccionar su entidad de la lista.\nTipo: %s | Categoria: %s\nPaciente: %s | Doc: %s",
			clientType, category, patientName, patientDoc)
		actions = "- Preguntale su entidad:\n" +
			"  /bot resume ASK_ENTITY_NUMBER — Mostrar lista de nuevo\n" +
			"  /bot resume ASK_CLIENT_TYPE — Cambiar tipo de entidad"

	case sm.StateConfirmEntity:
		patientEntity := sess.GetContext("patient_entity")
		situation = fmt.Sprintf("El paciente no pudo confirmar su entidad.\nEntidad actual: %s\nPaciente: %s | Doc: %s",
			patientEntity, patientName, patientDoc)
		actions = "- Verificale la entidad y luego:\n" +
			"  /bot resume CONFIRM_ENTITY — Preguntar confirmacion de nuevo\n" +
			"  /bot resume CHANGE_ENTITY — Cambiar entidad"

	case sm.StateChangeEntity:
		patientEntity := sess.GetContext("patient_entity")
		situation = fmt.Sprintf("El paciente no encontro su entidad al buscarla.\nEntidad actual: %s\nPaciente: %s | Doc: %s",
			patientEntity, patientName, patientDoc)
		actions = "- Preguntale el nombre de su entidad:\n" +
			"  /bot resume CHANGE_ENTITY — Reintentar busqueda\n" +
			"  /bot resume ASK_CLIENT_TYPE — Cambiar tipo de entidad"

	case sm.StateShowEntityList:
		clientType := sess.GetContext("client_type")
		category := sess.GetContext("entity_category")
		situation = fmt.Sprintf("No se encontraron entidades/EPS para la categoria seleccionada.\nTipo: %s | Categoria: %s\nPaciente: %s | Doc: %s",
			clientType, category, patientName, patientDoc)
		actions = "- Verifica la entidad del paciente y luego:\n" +
			"  /bot resume CHECK_ENTITY — Reintentar validacion de entidad\n" +
			"  /bot cerrar — Cerrar conversacion"

	// --- Registro de paciente ---
	case sm.StateRegistrationStart:
		situation = fmt.Sprintf("El paciente no decidio si registrarse como nuevo.\nDoc: %s", patientDoc)
		actions = "- Preguntale si quiere registrarse:\n" +
			"  /bot resume REGISTRATION_START — Preguntar de nuevo\n" +
			"  /bot resume ASK_DOCUMENT — Reintentar con otro documento\n" +
			"  /bot cerrar"

	case sm.StateConfirmRegistration:
		situation = fmt.Sprintf("El paciente no confirmo sus datos de registro.\nPaciente: %s | Doc: %s",
			sess.GetContext("reg_first_name")+" "+sess.GetContext("reg_first_surname"), patientDoc)
		actions = "- Verificale los datos y luego:\n" +
			"  /bot resume CONFIRM_REGISTRATION — Mostrar resumen de nuevo\n" +
			"  /bot resume REG_DOCUMENT_TYPE — Corregir desde el inicio"

	// --- Orden Medica ---
	case sm.StateAskMedicalOrder:
		situation = fmt.Sprintf("El paciente no eligio metodo para ingresar la orden (foto o manual).\nPaciente: %s | Doc: %s", patientName, patientDoc)
		actions = "- Preguntale como quiere ingresar la orden:\n" +
			"  /bot resume UPLOAD_MEDICAL_ORDER — Pedir foto de orden\n" +
			"  /bot resume ASK_MANUAL_CUPS nombre — Ingresar procedimiento manual"

	case sm.StateUploadMedicalOrder:
		situation = fmt.Sprintf("El paciente no logro enviar la foto de su orden medica.\nPaciente: %s | Doc: %s", patientName, patientDoc)
		actions = "- Pidele que envie la foto de la orden y luego:\n" +
			"  /bot resume UPLOAD_MEDICAL_ORDER — Reintentar subida de orden\n" +
			"- Si la imagen se ve pero el bot no la reconocio, describe la orden:\n" +
			"  /bot orden Resonancia cerebral simple codigo 883141, cantidad 1\n" +
			"  /bot orden Electromiografia 4 ext codigo 930810 cantidad 1, Resonancia columna lumbar codigo 883210 cantidad 1\n" +
			"- Si ya sabes el procedimiento:\n" +
			"  /bot resume ASK_MANUAL_CUPS nombre del procedimiento\n" +
			"  Ej: /bot resume ASK_MANUAL_CUPS resonancia cerebral"

	case sm.StateConfirmOCRResult:
		ocrCups := sess.GetContext("ocr_cups_json")
		situation = fmt.Sprintf("El paciente no confirmo el resultado del reconocimiento de la orden.\nCUPS detectados: %s\nPaciente: %s | Doc: %s",
			ocrCups, patientName, patientDoc)
		actions = "- Verificale los procedimientos detectados:\n" +
			"  /bot resume CONFIRM_OCR_RESULT — Mostrar resultado de nuevo\n" +
			"  /bot resume ASK_MANUAL_CUPS nombre — Corregir manualmente"

	case sm.StateAskManualCups:
		situation = fmt.Sprintf("El paciente no pudo encontrar su procedimiento al buscarlo.\nPaciente: %s | Doc: %s", patientName, patientDoc)
		actions = "- Preguntale el nombre del procedimiento:\n" +
			"  /bot resume ASK_MANUAL_CUPS nombre del procedimiento\n" +
			"  Ej: /bot resume ASK_MANUAL_CUPS resonancia cerebral simple\n" +
			"  Ej: /bot resume ASK_MANUAL_CUPS electromiografia"

	case sm.StateSelectProcedure:
		situation = fmt.Sprintf("El paciente no logro seleccionar un procedimiento de la lista.\nPaciente: %s | Doc: %s", patientName, patientDoc)
		actions = "- Preguntale que procedimiento necesita:\n" +
			"  /bot resume ASK_MANUAL_CUPS nombre del procedimiento\n" +
			"  Ej: /bot resume ASK_MANUAL_CUPS resonancia cerebral\n" +
			"  /bot resume SELECT_PROCEDURE — Mostrar lista de nuevo"

	// --- Validaciones Medicas ---
	case sm.StateAskGestationalWeeks:
		situation = fmt.Sprintf("Ecografia obstetrica — no ingreso semanas de gestacion (1-42).\nProcedimiento: %s (%s)\nPaciente: %s",
			cupsName, cupsCode, patientName)
		actions = "- Preguntale las semanas de gestacion:\n" +
			"  /bot resume ASK_GESTATIONAL_WEEKS 28 — Enviar semanas\n" +
			"  /bot resume ASK_GESTATIONAL_WEEKS — Preguntar de nuevo"

	case sm.StateAskContrasted:
		situation = fmt.Sprintf("El paciente no respondio si el examen es con o sin contraste.\nProcedimiento: %s (%s)\nPaciente: %s",
			cupsName, cupsCode, patientName)
		actions = "- Preguntale si es con o sin contraste:\n" +
			"  /bot resume ASK_CONTRASTED contrast_yes — Con contraste\n" +
			"  /bot resume ASK_CONTRASTED contrast_no — Sin contraste"

	case sm.StateAskPregnancy:
		situation = fmt.Sprintf("Paciente femenina con contraste — no respondio si esta embarazada.\nProcedimiento: %s\nPaciente: %s | Edad: %s",
			cupsName, patientName, sess.GetContext("patient_age"))
		actions = "- Preguntale si esta embarazada:\n" +
			"  /bot resume ASK_PREGNANCY pregnant_no — No embarazada\n" +
			"  /bot resume ASK_PREGNANCY pregnant_yes — Embarazada (bloquea cita)"

	case sm.StateAskBabyWeight:
		situation = fmt.Sprintf("Bebe <1 ano con contraste — no indico peso del bebe.\nPaciente: %s | Edad: %s",
			patientName, sess.GetContext("patient_age"))
		actions = "- Preguntale el peso del bebe:\n" +
			"  /bot resume ASK_BABY_WEIGHT baby_normal — Peso normal\n" +
			"  /bot resume ASK_BABY_WEIGHT baby_low — Bajo peso"

	case sm.StateGfrCreatinine:
		situation = fmt.Sprintf("El paciente no ingreso su valor de creatinina (examen con contraste).\nProcedimiento: %s\nPaciente: %s",
			cupsName, patientName)
		actions = "- Preguntale su creatinina (mg/dL):\n" +
			"  /bot resume GFR_CREATININE 0.96 — Enviar valor\n" +
			"  /bot resume GFR_CREATININE — Preguntar de nuevo"

	case sm.StateGfrHeight:
		situation = fmt.Sprintf("Paciente pediatrico — no ingreso estatura (30-250 cm).\nPaciente: %s | Edad: %s | Creatinina: %s",
			patientName, sess.GetContext("patient_age"), sess.GetContext("gfr_creatinine"))
		actions = "- Preguntale la estatura en cm:\n" +
			"  /bot resume GFR_HEIGHT 120 — Enviar estatura\n" +
			"  /bot resume GFR_HEIGHT — Preguntar de nuevo"

	case sm.StateGfrWeight:
		situation = fmt.Sprintf("El paciente no ingreso su peso (10-300 kg).\nPaciente: %s | Creatinina: %s",
			patientName, sess.GetContext("gfr_creatinine"))
		actions = "- Preguntale el peso en kg:\n" +
			"  /bot resume GFR_WEIGHT 70 — Enviar peso\n" +
			"  /bot resume GFR_WEIGHT — Preguntar de nuevo"

	case sm.StateGfrDisease:
		situation = fmt.Sprintf("El paciente (15-39 anos) no selecciono tipo de enfermedad para calculo GFR.\nPaciente: %s | Edad: %s | Creatinina: %s",
			patientName, sess.GetContext("patient_age"), sess.GetContext("gfr_creatinine"))
		actions = "- Preguntale si tiene alguna enfermedad:\n" +
			"  /bot resume GFR_DISEASE disease_none — Sin enfermedad\n" +
			"  /bot resume GFR_DISEASE disease_renal — Enfermedad renal\n" +
			"  /bot resume GFR_DISEASE disease_diabetica — Diabetes"

	case sm.StateAskSedation:
		situation = fmt.Sprintf("El paciente no respondio si requiere sedacion para resonancia.\nProcedimiento: %s (%s)\nPaciente: %s",
			cupsName, cupsCode, patientName)
		actions = "- Preguntale si necesita sedacion:\n" +
			"  /bot resume ASK_SEDATION sedated_yes — Con sedacion\n" +
			"  /bot resume ASK_SEDATION sedated_no — Sin sedacion"

	case sm.StateCheckSpecialCups:
		if isSleepStudy(cupsCode) {
			situation = fmt.Sprintf("Estudio del sueno — requiere coordinacion especial.\nProcedimiento: %s (%s)\nPaciente: %s | Doc: %s | Edad: %s",
				cupsName, cupsCode, patientName, patientDoc, sess.GetContext("patient_age"))
			actions = "- Coordina la cita manualmente y luego:\n" +
				"  /bot cerrar — Cerrar cuando termines"
		} else if isPETCT(cupsCode) {
			situation = fmt.Sprintf("PET/CT — requiere coordinacion con medicina nuclear.\nProcedimiento: %s (%s)\nPaciente: %s | Doc: %s",
				cupsName, cupsCode, patientName, patientDoc)
			actions = "- Coordina con medicina nuclear y luego:\n" +
				"  /bot cerrar — Cerrar cuando termines"
		} else {
			situation = fmt.Sprintf("Procedimiento especial requiere atencion manual.\nProcedimiento: %s (%s)\nPaciente: %s | Doc: %s",
				cupsName, cupsCode, patientName, patientDoc)
			actions = "- Gestiona el procedimiento manualmente:\n" +
				"  /bot cerrar — Cerrar cuando termines"
		}

	// --- Slots y Reserva ---
	case sm.StateShowSlots:
		situation = fmt.Sprintf("El paciente no logro seleccionar un horario para su cita.\nProcedimiento: %s (%s)", cupsName, cupsCode)
		actions = "- Preguntale que horario prefiere:\n" +
			"  /bot resume SHOW_SLOTS — Mostrar horarios de nuevo\n" +
			"  /bot resume SEARCH_SLOTS — Buscar nuevos horarios"

	case sm.StateOfferWaitingList:
		situation = fmt.Sprintf("No hay horarios disponibles — no respondio si quiere lista de espera.\nProcedimiento: %s\nPaciente: %s",
			cupsName, patientName)
		actions = "- Preguntale si quiere lista de espera:\n" +
			"  /bot resume OFFER_WAITING_LIST wl_yes — Agregar a lista\n" +
			"  /bot resume OFFER_WAITING_LIST wl_no — No agregar\n" +
			"  /bot resume SEARCH_SLOTS — Buscar horarios de nuevo"

	case sm.StateConfirmBooking:
		situation = fmt.Sprintf("El paciente no confirmo la reserva del horario seleccionado.\nProcedimiento: %s\nPaciente: %s",
			cupsName, patientName)
		actions = "- Preguntale si confirma:\n" +
			"  /bot resume CONFIRM_BOOKING booking_confirm — Confirmar\n" +
			"  /bot resume SEARCH_SLOTS — Buscar otros horarios"

	// --- Citas Existentes ---
	case sm.StateListAppointments:
		situation = fmt.Sprintf("El paciente no logro seleccionar una cita de la lista.\nPaciente: %s | Doc: %s", patientName, patientDoc)
		actions = "- Preguntale cual cita necesita gestionar:\n" +
			"  /bot resume LIST_APPOINTMENTS — Mostrar citas de nuevo\n" +
			"  /bot resume FETCH_APPOINTMENTS — Buscar citas de nuevo"

	case sm.StateAppointmentAction:
		situation = fmt.Sprintf("El paciente no selecciono accion sobre su cita.\nPaciente: %s | Cita: %s",
			patientName, sess.GetContext("selected_appointment_id"))
		actions = "- Preguntale que quiere hacer con la cita:\n" +
			"  /bot resume APPOINTMENT_ACTION appt_confirm — Confirmar cita\n" +
			"  /bot resume APPOINTMENT_ACTION appt_cancel — Cancelar cita\n" +
			"  /bot resume LIST_APPOINTMENTS — Volver a lista de citas"

	case sm.StateConfirmAppointment:
		situation = fmt.Sprintf("El paciente no confirmo la accion de confirmar cita.\nPaciente: %s", patientName)
		actions = "- Preguntale si confirma:\n" +
			"  /bot resume CONFIRM_APPOINTMENT — Mostrar confirmacion de nuevo\n" +
			"  /bot resume LIST_APPOINTMENTS — Volver a lista"

	case sm.StateCancelAppointment:
		situation = fmt.Sprintf("El paciente no confirmo la cancelacion de su cita.\nPaciente: %s", patientName)
		actions = "- Preguntale si desea cancelar:\n" +
			"  /bot resume CANCEL_APPOINTMENT — Mostrar confirmacion de nuevo\n" +
			"  /bot resume LIST_APPOINTMENTS — Volver a lista"

	case sm.StateCancelReason:
		situation = fmt.Sprintf("El paciente no selecciono motivo de cancelacion.\nPaciente: %s", patientName)
		actions = "- Preguntale el motivo:\n" +
			"  /bot resume CANCEL_REASON — Mostrar motivos de nuevo\n" +
			"  /bot resume LIST_APPOINTMENTS — Volver a lista"

	// --- Post-Accion y Cierre ---
	case sm.StatePostActionMenu:
		situation = "El paciente solicito hablar con un agente directamente."
		if menuOption != "" || cupsName != "" {
			details := []string{}
			if menuOption != "" {
				details = append(details, "Menu: "+menuOption)
			}
			if cupsName != "" {
				details = append(details, fmt.Sprintf("Procedimiento: %s (%s)", cupsName, cupsCode))
			}
			situation += "\n" + strings.Join(details, " | ")
		}
		actions = "- Atiende al paciente y luego:\n" +
			"  /bot cerrar — Cerrar conversacion\n" +
			"  /bot resume POST_ACTION_MENU — Devolver al menu de acciones"

	case sm.StateFallbackMenu:
		situation = "El paciente llego al menu de recuperacion (reintentos agotados o error)."
		if patientName != "" {
			situation += fmt.Sprintf("\nPaciente: %s | Doc: %s", patientName, patientDoc)
		}
		actions = "- Atiende al paciente y luego:\n" +
			"  /bot — Reiniciar desde cero\n" +
			"  /bot cerrar"

	default:
		// Check if it's a registration field state (REG_*)
		if label, ok := regFieldLabels[preState]; ok {
			situation = fmt.Sprintf("El paciente no pudo ingresar: %s\nEstado: %s\nPaciente: %s | Doc: %s",
				label, preState, patientName, patientDoc)
			actions = fmt.Sprintf("- Preguntale el dato y envialo:\n"+
				"  /bot resume %s dato — Enviar dato correcto\n"+
				"  /bot resume %s — Pedir dato de nuevo\n"+
				"  Ej: /bot resume %s valor_del_dato", preState, preState, preState)
		} else {
			situation = fmt.Sprintf("El paciente tuvo dificultades en el paso: %s", preState)
			if patientName != "" {
				situation += fmt.Sprintf("\nPaciente: %s | Doc: %s", patientName, patientDoc)
			}
			actions = fmt.Sprintf("- Preguntale que necesita y usa:\n"+
				"  /bot resume %s — Reintentar este paso\n"+
				"  /bot resume %s dato — Enviar dato corregido", preState, preState)
		}
	}

	return fmt.Sprintf("Situacion: %s\n\nAcciones sugeridas:\n%s\n\n"+
		"/bot — Reiniciar desde menu\n"+
		"/bot cerrar — Cerrar conversacion\n"+
		"/bot info — Ver contexto completo", situation, actions)
}
