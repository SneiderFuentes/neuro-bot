package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/repository/local"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
)

// RegisterMedicationHandlers registra los handlers del flujo de Aplicación de Medicamentos.
// medRepo puede ser nil (el bot informará que no hay listado disponible).
func RegisterMedicationHandlers(m *sm.Machine, medRepo *local.MedicationPendingRepo) {
	m.Register(sm.StateMedicationCheckExcel, medicationCheckExcelHandler(medRepo))
	m.Register(sm.StateMedicationAskLastDate, medicationAskLastDateHandler())
	m.Register(sm.StateMedicationAskDose, medicationAskDoseHandler())
	m.Register(sm.StateMedicationAskFrequency, medicationAskFrequencyHandler())
	m.RegisterWithConfig(sm.StateMedicationAskSymptoms, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"sin_sintomas", "con_sintomas"},
		RetryPrompt: func(_ *session.Session, result *sm.StateResult) {
			result.Messages = append(result.Messages, medicationSymptomsButtons())
		},
		Handler: medicationAskSymptomsHandler(),
	})
	m.Register(sm.StateMedicationReceiveHistoria, medicationReceiveHistoriaHandler())
	m.Register(sm.StateMedicationReceiveOrden, medicationReceiveOrdenHandler())
	m.Register(sm.StateMedicationPrepareSchedule, medicationPrepareScheduleHandler())
}

// MEDICATION_CHECK_EXCEL (automático) — busca al paciente en el listado de medicamentos pendientes.
func medicationCheckExcelHandler(medRepo *local.MedicationPendingRepo) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, _ bird.InboundMessage) (*sm.StateResult, error) {
		doc := sess.GetContext("patient_doc")

		if medRepo == nil {
			return backToMenuNoRecord(sess), nil
		}

		rec, err := medRepo.FindByDocument(ctx, doc)
		if err != nil || rec == nil {
			return backToMenuNoRecord(sess), nil
		}

		// Guardar datos del medicamento en sesión
		sess.SetContext("med_medicamento", rec.Medicamento)
		sess.SetContext("med_nombre_comercial", rec.NombreComercial)
		sess.SetContext("med_concentracion", rec.Concentracion)
		sess.SetContext("med_forma_farmaceutica", rec.FormaFarmaceutica)
		sess.SetContext("med_saldo", fmt.Sprintf("%d", rec.SaldoActual))

		// Mostrar info del medicamento y preguntar fecha de última aplicación
		medDesc := buildMedDescription(rec)
		msg := fmt.Sprintf(
			"✅ Encontramos tu medicamento en nuestro listado:\n\n%s\n\n"+
				"Para continuar con la solicitud necesito algunos datos clínicos.\n\n"+
				"📅 ¿Cuándo fue la *última vez* que te aplicaste este medicamento?\n"+
				"_(Escribe la fecha, ej: 15/08/2026 o \"hace 6 meses\")_",
			medDesc,
		)
		return sm.NewResult(sm.StateMedicationAskLastDate).
			WithText(msg).
			WithEvent("medication_excel_found", map[string]interface{}{"medicamento": rec.Medicamento}), nil
	}
}

func backToMenuNoRecord(sess *session.Session) *sm.StateResult {
	list := &sm.ListMessage{
		Body:  "Consultamos nuestro listado de medicamentos y *no encontramos un medicamento pendiente de aplicación* para tu documento.\n\n" +
			"Si crees que hay un error, comunícate directamente con la IPS para verificar tu registro.\n\n" +
			"¿En qué más puedo ayudarte?",
		Title: "Ver opciones",
		Sections: []sm.ListSection{{
			Title: "Menú principal",
			Rows: []sm.ListRow{
				{ID: "agendar", Title: "Agendar cita", Description: "Si tienes una orden médica"},
				{ID: "consultar", Title: "Citas Programadas", Description: "Consulta, confirma o cancela tus citas"},
				{ID: "ubicacion", Title: "Ubicación", Description: "Conoce nuestras sedes"},
				{ID: "ayuda", Title: "Cómo usar el bot", Description: "Guía rápida"},
			},
		}},
	}
	r := sm.NewResult(sm.StateMainMenu).
		WithClearCtx("medication_flow").
		WithEvent("medication_not_in_list", map[string]interface{}{"doc": sess.GetContext("patient_doc")})
	r.Messages = append(r.Messages, list)
	return r
}

func buildMedDescription(rec *local.MedicationPendingRecord) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("💊 *%s*", rec.Medicamento))
	if rec.NombreComercial != "" {
		parts = append(parts, fmt.Sprintf("   Nombre comercial: %s", rec.NombreComercial))
	}
	if rec.Concentracion != "" {
		parts = append(parts, fmt.Sprintf("   Concentración: %s", rec.Concentracion))
	}
	if rec.FormaFarmaceutica != "" {
		parts = append(parts, fmt.Sprintf("   Forma farmacéutica: %s", rec.FormaFarmaceutica))
	}
	if rec.EPS != "" {
		parts = append(parts, fmt.Sprintf("   EPS: %s", rec.EPS))
	}
	return strings.Join(parts, "\n")
}

// MEDICATION_ASK_LAST_DATE (interactivo) — recibe la fecha de última aplicación.
func medicationAskLastDateHandler() sm.StateHandler {
	return func(_ context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			return sm.NewResult(sess.CurrentState).
				WithText("Por favor escribe la fecha de tu última aplicación. Ejemplo: *15/08/2026* o *\"hace 6 meses\"*."), nil
		}
		sess.SetContext("med_last_date", text)
		return sm.NewResult(sm.StateMedicationAskDose).
			WithText("💉 ¿Cuál es la *dosis* que te aplicas?\n_(Ej: 1 jeringa de 40mg, 2 viales, etc.)_"), nil
	}
}

// MEDICATION_ASK_DOSE (interactivo) — recibe la dosis.
func medicationAskDoseHandler() sm.StateHandler {
	return func(_ context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			return sm.NewResult(sess.CurrentState).
				WithText("Por favor escribe la dosis. Ejemplo: *1 jeringa de 40mg*."), nil
		}
		sess.SetContext("med_dose", text)
		return sm.NewResult(sm.StateMedicationAskFrequency).
			WithText("🔁 ¿Con qué *frecuencia* te aplicas el medicamento?\n_(Ej: cada mes, cada 3 meses, semanal, etc.)_"), nil
	}
}

// MEDICATION_ASK_FREQUENCY (interactivo) — recibe la frecuencia y transiciona a síntomas.
func medicationAskFrequencyHandler() sm.StateHandler {
	return func(_ context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			return sm.NewResult(sess.CurrentState).
				WithText("Por favor escribe la frecuencia. Ejemplo: *cada mes*."), nil
		}
		sess.SetContext("med_frequency", text)

		r := sm.NewResult(sm.StateMedicationAskSymptoms).
			WithText("🌡️ *Revisión de síntomas*\n\n" +
				"En la *última semana*, ¿has presentado alguno de estos síntomas?\n\n" +
				"• Fiebre\n• Vómito\n• Diarrea\n• Congestión nasal")
		r.Messages = append(r.Messages, medicationSymptomsButtons())
		return r, nil
	}
}

func medicationSymptomsButtons() *sm.ButtonMessage {
	return &sm.ButtonMessage{
		Text: "Selecciona una opción:",
		Buttons: []sm.Button{
			{Text: "✅ No, ninguno", Payload: "sin_sintomas"},
			{Text: "❌ Sí, tengo alguno", Payload: "con_sintomas"},
		},
	}
}

// MEDICATION_ASK_SYMPTOMS (interactivo) — valida síntomas con botones.
func medicationAskSymptomsHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, _ bird.InboundMessage) (*sm.StateResult, error) {
		switch sm.ValidatedPayload(ctx) {
		case "con_sintomas":
			list := &sm.ListMessage{
				Body: "⚠️ Lo sentimos, por tu seguridad *no podemos aplicar el medicamento* mientras presentas estos síntomas.\n\n" +
					"Te recomendamos consultar a tu médico tratante y, una vez recuperado/a, comunicarte nuevamente con nosotros.\n\n" +
					"¿En qué más puedo ayudarte?",
				Title: "Ver opciones",
				Sections: []sm.ListSection{{
					Title: "Menú principal",
					Rows: []sm.ListRow{
						{ID: "agendar", Title: "Agendar cita", Description: "Si tienes una orden médica"},
						{ID: "consultar", Title: "Citas Programadas", Description: "Consulta o cancela citas"},
						{ID: "ubicacion", Title: "Ubicación", Description: "Conoce nuestras sedes"},
					},
				}},
			}
			r := sm.NewResult(sm.StateMainMenu).
				WithClearCtx("medication_flow").
				WithEvent("medication_symptoms_present", nil)
			r.Messages = append(r.Messages, list)
			return r, nil

		case "sin_sintomas":
			return sm.NewResult(sm.StateMedicationReceiveHistoria).
				WithText("📋 Perfecto. Ahora necesito que envíes los documentos.\n\n" +
					"1️⃣ *Historia clínica* — envía una foto o PDF."), nil

		default:
			r := sm.NewResult(sess.CurrentState)
			r.Messages = append(r.Messages, medicationSymptomsButtons())
			return r, nil
		}
	}
}

// MEDICATION_RECEIVE_HISTORIA (interactivo) — recibe foto/PDF de la historia clínica.
func medicationReceiveHistoriaHandler() sm.StateHandler {
	return func(_ context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		mediaURL := mediaURLFromMessage(msg)
		if mediaURL == "" {
			return sm.NewResult(sess.CurrentState).
				WithText("Por favor envía la *historia clínica* como foto o PDF."), nil
		}
		sess.SetContext("med_historia_url", mediaURL)
		return sm.NewResult(sm.StateMedicationReceiveOrden).
			WithText("✅ Historia clínica recibida.\n\n" +
				"2️⃣ Ahora envía la *orden médica* (foto o PDF)."), nil
	}
}

// MEDICATION_RECEIVE_ORDEN (interactivo) — recibe foto/PDF de la orden médica.
func medicationReceiveOrdenHandler() sm.StateHandler {
	return func(_ context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		mediaURL := mediaURLFromMessage(msg)
		if mediaURL == "" {
			return sm.NewResult(sess.CurrentState).
				WithText("Por favor envía la *orden médica* como foto o PDF."), nil
		}
		sess.SetContext("med_orden_url", mediaURL)
		return sm.NewResult(sm.StateMedicationPrepareSchedule).
			WithText("✅ Documentos recibidos. Buscando disponibilidad para tu cita de aplicación... 🔍"), nil
	}
}

// MEDICATION_PREPARE_SCHEDULE (automático) — configura cups_code y procedures_json para StateSearchSlots.
func medicationPrepareScheduleHandler() sm.StateHandler {
	return func(_ context.Context, sess *session.Session, _ bird.InboundMessage) (*sm.StateResult, error) {
		const cupsCode = "992990"
		const cupsName = "INYECCIÓN O INFUSIÓN DE OTRA SUSTANCIA TERAPÉUTICA O PROFILÁCTICA"

		group := services.CUPSGroup{
			ServiceType: "Aplicacion Medicamentos",
			Cups: []services.CUPSEntry{
				{Code: cupsCode, Name: cupsName, Quantity: 1},
			},
			Espacios: 1,
		}
		procJSON, err := json.Marshal([]services.CUPSGroup{group})
		if err != nil {
			return sm.NewResult(sm.StateEscalateToAgent).
				WithText("Ocurrió un error preparando tu solicitud. Te conecto con un agente. 😊").
				WithContext("escalation_reason", "medicamentos_error"), nil
		}

		return sm.NewResult(sm.StateSearchSlots).
			WithContext("cups_code", cupsCode).
			WithContext("procedures_json", string(procJSON)).
			WithContext("current_procedure_idx", "0").
			WithClearCtx("medication_flow").
			WithEvent("medication_prepare_schedule", map[string]interface{}{
				"medicamento": sess.GetContext("med_medicamento"),
			}), nil
	}
}

func mediaURLFromMessage(msg bird.InboundMessage) string {
	switch msg.MessageType {
	case "image":
		return msg.ImageURL
	case "document":
		return msg.DocumentURL
	}
	return ""
}
