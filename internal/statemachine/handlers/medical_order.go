package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/repository"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
)

// RegisterMedicalOrderHandlers registra los handlers de Orden Médica y OCR (Fase 8)
func RegisterMedicalOrderHandlers(m *sm.Machine, ocrSvc *services.OCRService, procedureRepo repository.ProcedureRepository) {
	m.Register(sm.StateAskMedicalOrder, askMedicalOrderHandler())
	m.Register(sm.StateUploadMedicalOrder, uploadMedicalOrderHandler(ocrSvc))
	m.Register(sm.StateValidateOCR, validateOCRHandler(procedureRepo))
	m.Register(sm.StateConfirmOCRResult, confirmOCRResultHandler(procedureRepo))
	m.Register(sm.StateOCRFailed, ocrFailedHandler())
	m.Register(sm.StateAskManualCups, askManualCupsHandler(procedureRepo))
	m.Register(sm.StateSelectProcedure, selectProcedureHandler())
}

// ASK_MEDICAL_ORDER (automático) — pide foto de la orden y transiciona a UPLOAD.
func askMedicalOrderHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		return sm.NewResult(sm.StateUploadMedicalOrder).
			WithText("Envía una *foto clara* o *PDF* de tu orden médica.\n\nAsegúrate de que:\n- Se vean bien los procedimientos\n- La foto no esté borrosa\n- Se lea el texto").
			WithEvent("order_photo_requested", nil), nil
	}
}

// UPLOAD_MEDICAL_ORDER (interactivo) — espera imagen, procesa OCR
func uploadMedicalOrderHandler(ocrSvc *services.OCRService) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		switch msg.MessageType {
		case "image", "document":
			mediaURL := msg.ImageURL
			if msg.MessageType == "document" {
				mediaURL = msg.DocumentURL
			}
			if mediaURL == "" {
				return sm.NewResult(sess.CurrentState).
					WithText("No pude obtener el archivo. Por favor envía otra foto o PDF."), nil
			}

			ocrResult, err := ocrSvc.AnalyzeDocument(ctx, mediaURL)
			if err != nil {
				return sm.NewResult(sess.CurrentState).
					WithButtons("Error al procesar la imagen. ¿Qué deseas hacer?",
						sm.Button{Text: "Enviar de nuevo", Payload: "retry_photo"},
						sm.Button{Text: "Hablar con agente", Payload: "escalate_agent"},
					).
					WithEvent("ocr_error", map[string]interface{}{"error": err.Error()}), nil
			}

			if !ocrResult.Success || len(ocrResult.Cups) == 0 {
				errorMsg := "No pude leer procedimientos en esta imagen."
				if ocrResult.Error != "" {
					errorMsg += "\n\nDetalle: " + ocrResult.Error
				}
				return sm.NewResult(sess.CurrentState).
					WithButtons(errorMsg+"\n\n¿Qué deseas hacer?",
						sm.Button{Text: "Enviar de nuevo", Payload: "retry_photo"},
						sm.Button{Text: "Hablar con agente", Payload: "escalate_agent"},
					).
					WithEvent("ocr_failed", map[string]interface{}{"error": ocrResult.Error}), nil
			}

			// OCR exitoso — guardar CUPS en contexto
			cupsJSON, _ := json.Marshal(ocrResult.Cups)

			r := sm.NewResult(sm.StateValidateOCR).
				WithContext("ocr_cups_json", string(cupsJSON)).
				WithEvent("ocr_success", map[string]interface{}{"cups_count": len(ocrResult.Cups)})

			// Guardar documento extraído para verificación posterior
			if ocrResult.Document != "" {
				r.WithContext("ocr_document", ocrResult.Document)
			}

			// Alerta de Capital Salud
			if strings.EqualFold(ocrResult.Entity, "Capital Salud") {
				r.WithText("Detectamos que tu EPS es *Capital Salud*. Ten en cuenta que actualmente no tenemos convenio con esta entidad. El servicio sería *particular*.")
			}

			return r, nil

		default:
			// Texto u otro tipo de mensaje
			if msg.IsPostback {
				switch msg.PostbackPayload {
				case "retry_photo":
					return sm.NewResult(sess.CurrentState).
						WithText("Envía otra foto o PDF de tu orden médica."), nil
				case "escalate_agent":
					return sm.NewResult(sm.StateEscalateToAgent).
						WithText("Te voy a comunicar con uno de nuestros agentes para que pueda ayudarte con tu orden médica.").
						WithEvent("ocr_escalate_to_agent", nil), nil
				}
			}

			return sm.RetryOrEscalate(sess, "Estoy esperando una *foto o PDF* de tu orden médica. Por favor envía el archivo."), nil
		}
	}
}

// VALIDATE_OCR (automático) — muestra resultados del OCR con verificación de documento
func validateOCRHandler(procedureRepo repository.ProcedureRepository) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		var cups []services.CUPSEntry
		if err := json.Unmarshal([]byte(sess.GetContext("ocr_cups_json")), &cups); err != nil {
			return sm.NewResult(sm.StateEscalateToAgent).
				WithText("Error al procesar los datos de tu orden. Te voy a comunicar con un agente para que pueda ayudarte.").
				WithEvent("ocr_parse_error", map[string]interface{}{"error": err.Error()}), nil
		}

		// Validar CUPS contra la BD — enriquecer con nombre de BD si el código es conocido
		for i, cup := range cups {
			if cup.Code != "" {
				proc, err := procedureRepo.FindByCode(ctx, cup.Code)
				if err == nil && proc != nil {
					cups[i].Name = proc.Name
				}
			}
		}

		// Re-serializar con datos enriquecidos
		cupsJSON, _ := json.Marshal(cups)

		// Construir resumen
		summary := "Detecté los siguientes procedimientos en tu orden:\n\n"
		for i, cup := range cups {
			if cup.Code != "" {
				summary += fmt.Sprintf("%d. *%s* (CUPS: %s)", i+1, cup.Name, cup.Code)
			} else {
				summary += fmt.Sprintf("%d. *%s*", i+1, cup.Name)
			}
			if cup.Quantity > 1 {
				summary += fmt.Sprintf(" x %d", cup.Quantity)
			}
			summary += "\n"
		}

		// Verificar documento: comparar con el del paciente identificado
		ocrDoc := sess.GetContext("ocr_document")
		patientDoc := sess.GetContext("patient_document")
		if ocrDoc != "" && patientDoc != "" && ocrDoc != patientDoc {
			summary += fmt.Sprintf("\n⚠️ *Atención:* El documento en la orden (%s) no coincide con el que ingresaste (%s). Verifica que la orden sea tuya.\n", ocrDoc, patientDoc)
		}

		summary += "\n¿Es correcto?"

		return sm.NewResult(sm.StateConfirmOCRResult).
			WithContext("ocr_cups_json", string(cupsJSON)).
			WithButtons(summary,
				sm.Button{Text: "Sí, correcto", Payload: "ocr_correct"},
				sm.Button{Text: "No, corregir", Payload: "ocr_incorrect"},
			), nil
	}
}

// CONFIRM_OCR_RESULT (interactivo) — usuario confirma o corrige
func confirmOCRResultHandler(procedureRepo repository.ProcedureRepository) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "ocr_correct", "ocr_incorrect")
		if result != nil {
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: "¿Es correcto?",
				Buttons: []sm.Button{
					{Text: "Sí, correcto", Payload: "ocr_correct"},
					{Text: "No, corregir", Payload: "ocr_incorrect"},
				},
			})
			return result, nil
		}

		switch selected {
		case "ocr_correct":
			var cups []services.CUPSEntry
			if err := json.Unmarshal([]byte(sess.GetContext("ocr_cups_json")), &cups); err != nil {
				return sm.NewResult(sm.StateEscalateToAgent).
					WithText("Error al procesar tu orden. Te voy a comunicar con un agente para que pueda ayudarte.").
					WithEvent("ocr_parse_error", map[string]interface{}{"error": err.Error()}), nil
			}

			// Agrupar por servicio usando reglas institucionales desde BD
			// (Fisiatría EMG/NC + Resonancia magnética con reglas de espacios y combinaciones)
			groups, err := services.GroupByServiceFromDB(ctx, cups, procedureRepo)
			if err != nil || len(groups) == 0 {
				// Fallback: cada CUPS es un grupo individual
				groups = make([]services.CUPSGroup, len(cups))
				for i, c := range cups {
					groups[i] = services.CUPSGroup{
						ServiceType: "General",
						Cups:        []services.CUPSEntry{c},
						Espacios:    c.Quantity,
					}
				}
			}

			// Separar grupos con múltiples CUPS en grupos individuales.
			// Excepciones (se mantienen juntos en una sola cita):
			//   - Fisiatría: EMG + NC van en la misma agenda
			//   - Resonancia: combinaciones (ej. abdomen+pelvis) van en la misma cita
			var splitGroups []services.CUPSGroup
			for _, g := range groups {
				isFisiatria := strings.EqualFold(g.ServiceType, "Fisiatria") || strings.EqualFold(g.ServiceType, "Fisiatría")
				isResonancia := strings.EqualFold(g.ServiceType, "Resonancia")
				if isFisiatria || isResonancia || len(g.Cups) <= 1 {
					splitGroups = append(splitGroups, g)
					continue
				}
				// Grupos de otros servicios con múltiples CUPS → una cita por CUPS
				for _, c := range g.Cups {
					espacios := c.Quantity
					if espacios < 1 {
						espacios = 1
					}
					splitGroups = append(splitGroups, services.CUPSGroup{
						ServiceType: g.ServiceType,
						Cups:        []services.CUPSEntry{c},
						Espacios:    espacios,
					})
				}
			}
			groups = splitGroups

			groupsJSON, _ := json.Marshal(groups)

			r := sm.NewResult(sm.StateCheckSpecialCups).
				WithContext("procedures_json", string(groupsJSON)).
				WithContext("total_procedures", fmt.Sprintf("%d", len(groups))).
				WithContext("current_procedure_idx", "0")

			if len(groups) > 1 {
				summaryText := fmt.Sprintf("Tu orden tiene *%d grupo(s) de procedimientos*:\n\n", len(groups))
				for i, g := range groups {
					cupNames := make([]string, len(g.Cups))
					for j, c := range g.Cups {
						cupNames[j] = c.Name
					}
					summaryText += fmt.Sprintf("%d. %s (%s)\n", i+1, g.ServiceType, strings.Join(cupNames, ", "))
				}
				summaryText += "\nProcesaremos cada grupo por separado."
				r.WithText(summaryText)
			}

			// Cargar primer grupo como CUPS actual
			firstGroup := groups[0]
			
			// Para grupos con múltiples CUPS (Fisiatría, Resonancia), guardar códigos alternativos
			// para que la búsqueda de slots pueda probar con cualquiera del grupo.
			cupsForSearch := firstGroup.Cups[0]
			if len(firstGroup.Cups) > 1 {
				alternativeCodes := make([]string, 0, len(firstGroup.Cups)-1)
				for i, c := range firstGroup.Cups {
					if i == 0 {
						continue // El primero ya está en cups_code
					}
					alternativeCodes = append(alternativeCodes, c.Code)
				}
				if len(alternativeCodes) > 0 {
					r.WithContext("alternative_cups_codes", strings.Join(alternativeCodes, ","))
				}
			}
			
			r.WithContext("cups_code", cupsForSearch.Code).
				WithContext("cups_name", cupsForSearch.Name).
				WithContext("espacios", fmt.Sprintf("%d", firstGroup.Espacios))

			// Propagar is_sedated y is_contrasted del primer grupo si algún CUPS lo tiene
			for _, c := range firstGroup.Cups {
				if c.IsSedated {
					r.WithContext("ocr_is_sedated", "1")
					break
				}
			}
			for _, c := range firstGroup.Cups {
				if c.IsContrasted {
					r.WithContext("ocr_is_contrasted", "1")
					break
				}
			}

			return r.WithEvent("ocr_validated", map[string]interface{}{"groups": len(groups)}), nil

		case "ocr_incorrect":
			return sm.NewResult(sm.StateUploadMedicalOrder).
				WithButtons("Entendido. ¿Qué deseas hacer?",
					sm.Button{Text: "Enviar de nuevo", Payload: "retry_photo"},
					sm.Button{Text: "Hablar con agente", Payload: "escalate_agent"},
				).
				WithClearCtx("ocr_cups_json").
				WithEvent("ocr_rejected", nil), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// OCR_FAILED (automático) — error en OCR, redirige
func ocrFailedHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		return sm.NewResult(sm.StateUploadMedicalOrder).
			WithButtons("No pudimos procesar tu orden médica. ¿Qué deseas hacer?",
				sm.Button{Text: "Enviar de nuevo", Payload: "retry_photo"},
				sm.Button{Text: "Hablar con agente", Payload: "escalate_agent"},
			).
			WithEvent("ocr_failed_redirect", nil), nil
	}
}

// ASK_MANUAL_CUPS (interactivo) — usuario escribe nombre del procedimiento
func askManualCupsHandler(procedureRepo repository.ProcedureRepository) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		input := strings.TrimSpace(msg.Text)

		if len(input) < 3 {
			retryResult := sm.ValidateWithRetry(sess, input, func(s string) bool { return len(s) >= 3 },
				"Por favor escribe al menos 3 caracteres del nombre del procedimiento.")
			return retryResult, nil
		}

		// Buscar en catálogo de CUPS
		procs, err := procedureRepo.SearchByName(ctx, input)
		if err != nil {
			return sm.NewResult(sess.CurrentState).
				WithText("Error al buscar procedimientos. Intenta de nuevo.").
				WithEvent("procedure_search_error", map[string]interface{}{"error": err.Error()}), nil
		}

		if len(procs) == 0 {
			return sm.NewResult(sess.CurrentState).
				WithText("No encontré procedimientos con ese nombre. Intenta con otro término.\n\nEjemplo: \"Electromiografía\", \"Resonancia\", \"Potenciales evocados\"").
				WithEvent("procedure_not_found", map[string]interface{}{"query": input}), nil
		}

		if len(procs) == 1 {
			// Un solo resultado → auto-seleccionar
			proc := procs[0]
			espacios := proc.RequiredSpaces
			if espacios < 1 {
				espacios = 1
			}

			// Construir procedures_json para evitar contexto stale de sesiones previas
			singleGroup := services.CUPSGroup{
				ServiceType: "General",
				Cups:        []services.CUPSEntry{{Code: proc.Code, Name: proc.Name, Quantity: 1}},
				Espacios:    espacios,
			}
			groupsJSON, _ := json.Marshal([]services.CUPSGroup{singleGroup})

			return sm.NewResult(sm.StateCheckSpecialCups).
				WithContext("cups_code", proc.Code).
				WithContext("cups_name", proc.Name).
				WithContext("espacios", fmt.Sprintf("%d", espacios)).
				WithContext("total_procedures", "1").
				WithContext("current_procedure_idx", "0").
				WithContext("procedures_json", string(groupsJSON)).
				WithClearCtx("ocr_cups_json").
				WithText(fmt.Sprintf("Procedimiento seleccionado: *%s* (CUPS: %s)", proc.Name, proc.Code)).
				WithEvent("manual_cups_selected", map[string]interface{}{
					"code": proc.Code,
					"name": proc.Name,
				}), nil
		}

		// Múltiples resultados → mostrar lista
		procsJSON, _ := json.Marshal(procs)

		rows := make([]sm.ListRow, len(procs))
		for i, p := range procs {
			desc := p.ServiceName
			if desc == "" {
				desc = p.Code
			}
			rows[i] = sm.ListRow{
				ID:          fmt.Sprintf("%d", p.ID),
				Title:       truncate(p.Name, 24),
				Description: truncate(desc, 72),
			}
		}

		return sm.NewResult(sm.StateSelectProcedure).
			WithContext("search_procedures_json", string(procsJSON)).
			WithList(
				fmt.Sprintf("Encontré *%d procedimientos*.\nSelecciona el correcto:", len(procs)),
				"Ver procedimientos",
				sm.ListSection{Title: "Procedimientos", Rows: rows},
			).
			WithEvent("procedure_search_results", map[string]interface{}{"count": len(procs)}), nil
	}
}

// SELECT_PROCEDURE (interactivo) — selección de procedimiento de la lista
func selectProcedureHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		if !msg.IsPostback {
			result := sm.RetryOrEscalate(sess, "Por favor selecciona un procedimiento de la lista, o escribe otro nombre para buscar de nuevo.")
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}
			return sm.NewResult(sm.StateAskManualCups).
				WithText("Por favor selecciona un procedimiento de la lista, o escribe otro nombre para buscar de nuevo."), nil
		}

		selectedID := msg.PostbackPayload

		// Buscar el procedimiento seleccionado
		var procs []struct {
			ID             int    `json:"ID"`
			Code           string `json:"Code"`
			Name           string `json:"Name"`
			ServiceName    string `json:"ServiceName"`
			RequiredSpaces int    `json:"RequiredSpaces"`
		}
		if err := json.Unmarshal([]byte(sess.GetContext("search_procedures_json")), &procs); err != nil {
			return sm.NewResult(sm.StateAskManualCups).
				WithText("Error al cargar procedimientos. Escribe el nombre de nuevo.").
				WithClearCtx("search_procedures_json"), nil
		}

		var selected *struct {
			ID             int    `json:"ID"`
			Code           string `json:"Code"`
			Name           string `json:"Name"`
			ServiceName    string `json:"ServiceName"`
			RequiredSpaces int    `json:"RequiredSpaces"`
		}
		for i, p := range procs {
			if fmt.Sprintf("%d", p.ID) == selectedID {
				selected = &procs[i]
				break
			}
		}

		if selected == nil {
			result := sm.RetryOrEscalate(sess, "Procedimiento no encontrado. Escribe el nombre de nuevo.")
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}
			return sm.NewResult(sm.StateAskManualCups).
				WithText("Procedimiento no encontrado. Escribe el nombre de nuevo.").
				WithClearCtx("search_procedures_json"), nil
		}

		espacios := selected.RequiredSpaces
		if espacios < 1 {
			espacios = 1
		}

		// Construir procedures_json para evitar contexto stale de sesiones previas
		singleGroup := services.CUPSGroup{
			ServiceType: "General",
			Cups:        []services.CUPSEntry{{Code: selected.Code, Name: selected.Name, Quantity: 1}},
			Espacios:    espacios,
		}
		groupsJSON, _ := json.Marshal([]services.CUPSGroup{singleGroup})

		return sm.NewResult(sm.StateCheckSpecialCups).
			WithContext("cups_code", selected.Code).
			WithContext("cups_name", selected.Name).
			WithContext("espacios", fmt.Sprintf("%d", espacios)).
			WithContext("total_procedures", "1").
			WithContext("current_procedure_idx", "0").
			WithContext("procedures_json", string(groupsJSON)).
			WithClearCtx("search_procedures_json", "ocr_cups_json").
			WithText(fmt.Sprintf("Procedimiento seleccionado: *%s* (CUPS: %s)", selected.Name, selected.Code)).
			WithEvent("manual_cups_selected", map[string]interface{}{
				"code": selected.Code,
				"name": selected.Name,
			}), nil
	}
}
