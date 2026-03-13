package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/repository"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	sm "github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/utils"
)

// WaitingListCreator is the interface needed by the OFFER_WAITING_LIST handler.
type WaitingListCreator interface {
	Create(ctx context.Context, entry *domain.WaitingListEntry) error
	HasActiveForPatientAndCups(ctx context.Context, patientID, cupsCode string) (bool, error)
}

// advanceToNextProcedure checks if there are more procedure groups to process.
// If yes, returns a result that transitions to the next group. If no, returns nil.
func advanceToNextProcedure(sess *session.Session) *sm.StateResult {
	totalProc, _ := strconv.Atoi(sess.GetContext("total_procedures"))
	currentIdx, _ := strconv.Atoi(sess.GetContext("current_procedure_idx"))

	if currentIdx+1 >= totalProc {
		return nil
	}

	nextIdx := currentIdx + 1
	var groups []services.CUPSGroup
	if err := json.Unmarshal([]byte(sess.GetContext("procedures_json")), &groups); err != nil {
		return nil
	}
	if nextIdx >= len(groups) || len(groups[nextIdx].Cups) == 0 {
		return nil
	}

	nextGroup := groups[nextIdx]
	r := sm.NewResult(sm.StateCheckSpecialCups).
		WithText(fmt.Sprintf("Ahora procesaremos el siguiente procedimiento:\n*%s*", nextGroup.Cups[0].Name)).
		WithContext("current_procedure_idx", fmt.Sprintf("%d", nextIdx)).
		WithContext("cups_code", nextGroup.Cups[0].Code).
		WithContext("cups_name", nextGroup.Cups[0].Name).
		WithContext("espacios", fmt.Sprintf("%d", nextGroup.Espacios)).
		WithClearCtx("is_contrasted", "is_sedated", "is_pregnant",
			"gfr_creatinine", "gfr_height_cm", "gfr_weight_kg",
			"gfr_disease_type", "gfr_calculated",
			"selected_slot_id", "available_slots_json", "slots_after_date",
			"preferred_doctor_doc", "ocr_is_sedated", "ocr_is_contrasted",
			"_prompted_contrast", "_prompted_sedation", "_prompted_pregnancy",
			"cups_preparation", "cups_video_url", "cups_audio_url")

	// Propagate OCR sedation/contrast detection for next group
	for _, c := range nextGroup.Cups {
		if c.IsSedated {
			r.WithContext("ocr_is_sedated", "1")
			break
		}
	}
	for _, c := range nextGroup.Cups {
		if c.IsContrasted {
			r.WithContext("ocr_is_contrasted", "1")
			break
		}
	}
	return r
}

// RegisterSlotHandlers registra los 8 handlers de búsqueda de slots y agendamiento (Fase 10).
func RegisterSlotHandlers(
	m *sm.Machine,
	slotSvc *services.SlotService,
	apptSvc *services.AppointmentService,
	procRepo repository.ProcedureRepository,
	soatRepo repository.SoatRepository,
	waitingListRepo WaitingListCreator,
	addrMapper *services.AddressMapper,
) {
	m.Register(sm.StateSearchSlots, searchSlotsHandler(slotSvc, apptSvc, procRepo))
	m.Register(sm.StateShowSlots, showSlotsHandler(addrMapper))
	m.Register(sm.StateNoSlotsAvailable, noSlotsHandler(waitingListRepo))
	m.Register(sm.StateOfferWaitingList, offerWaitingListHandler(waitingListRepo))
	m.RegisterWithConfig(sm.StateConfirmBooking, sm.HandlerConfig{
		InputType: sm.InputButton,
		Options:   []string{"booking_confirm", "booking_change"},
		RetryPrompt: func(sess *session.Session, result *sm.StateResult) {
			slot := findSelectedSlot(sess)
			if slot == nil {
				result.NextState = sm.StateSearchSlots
				result.Messages = []sm.OutboundMessage{&sm.TextMessage{Text: "Slot no encontrado. Buscando nuevos horarios..."}}
				result.ClearCtx = append(result.ClearCtx, "selected_slot_id", "available_slots_json")
				return
			}
			summary := buildBookingSummary(sess, slot, addrMapper)
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: summary,
				Buttons: []sm.Button{
					{Text: "Confirmar cita", Payload: "booking_confirm"},
					{Text: "Elegir otro", Payload: "booking_change"},
				},
			})
		},
		Handler: confirmBookingHandler(),
	})
	m.Register(sm.StateReconfirmBooking, reconfirmBookingHandler(addrMapper))
	m.Register(sm.StateCreateAppointment, createAppointmentHandler(apptSvc, soatRepo))
	m.Register(sm.StateBookingSuccess, bookingSuccessHandler(addrMapper))
	m.Register(sm.StateBookingFailed, bookingFailedHandler())
}

// SEARCH_SLOTS (automático) — busca slots disponibles con todos los filtros.
func searchSlotsHandler(slotSvc *services.SlotService, apptSvc *services.AppointmentService, procRepo repository.ProcedureRepository) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		cupsCode := sess.GetContext("cups_code")
		age, _ := strconv.Atoi(sess.GetContext("patient_age"))
		isContrasted := sess.GetContext("is_contrasted") == "1"
		isSedated := sess.GetContext("is_sedated") == "1"
		espacios, _ := strconv.Atoi(sess.GetContext("espacios"))
		if espacios == 0 {
			espacios = 1
		}

		// Look up procedure details (address, preparation, video, type)
		var address string
		var procedureType string
		if procRepo != nil {
			proc, _ := procRepo.FindByCode(ctx, cupsCode)
			if proc != nil {
				address = proc.Address
				procedureType = proc.Type
				if proc.Preparation != "" {
					sess.SetContext("cups_preparation", proc.Preparation)
				}
				if proc.VideoURL != "" {
					sess.SetContext("cups_video_url", proc.VideoURL)
				}
				if proc.AudioURL != "" {
					sess.SetContext("cups_audio_url", proc.AudioURL)
				}
			}
		}

		// Sedation override: force agenda type to "sedacion" (same as Laravel)
		if isSedated {
			procedureType = "sedacion"
		}

		// Store procedure type in session for waiting list entries
		if procedureType != "" {
			sess.SetContext("procedure_type", procedureType)
		}

		query := services.SlotQuery{
			CupsCode:        cupsCode,
			PatientAge:      age,
			IsContrasted:    isContrasted,
			IsSedated:       isSedated,
			Espacios:        espacios,
			PreferredDoctor: sess.GetContext("preferred_doctor_doc"),
			AfterDate:       sess.GetContext("slots_after_date"),
			MaxSlots:        5,
			ClinicAddress:   address,
			ProcedureType:   procedureType,
		}

		// SOAT monthly limit filter (SAN01 + soatGroup CUPS)
		if sess.GetContext("soat_limit_check") == "1" && apptSvc != nil {
			entity := sess.GetContext("patient_entity")
			query.MonthFilter = func(year, month int) (bool, error) {
				blocked, err := apptSvc.CheckSOATLimitForMonth(ctx, cupsCode, entity, year, month)
				if err != nil {
					return true, nil // fail-open
				}
				return !blocked, nil
			}
		}

		slots, err := slotSvc.GetAvailableSlots(ctx, query)
		if err != nil {
			r := sm.NewResult(sm.StatePostActionMenu).
				WithText("Error al buscar horarios. Intenta más tarde.")
			r.Messages = append(r.Messages, buildPostActionList("¿Qué deseas hacer?"))
			return r.WithEvent("slot_search_error", map[string]interface{}{"error": err.Error()}), nil
		}

		if len(slots) == 0 {
			return sm.NewResult(sm.StateNoSlotsAvailable).
				WithEvent("no_slots_found", map[string]interface{}{"cups_code": cupsCode}), nil
		}

		slotsJSON, _ := json.Marshal(slots)
		cupsName := sess.GetContext("cups_name")

		// Build list for SHOW_SLOTS
		rows := make([]sm.ListRow, 0, len(slots)+1)
		for _, slot := range slots {
			rows = append(rows, sm.ListRow{
				ID:          slot.TimeSlot,
				Title:       fmt.Sprintf("%s - %s", utils.FormatFriendlyDateShortStr(slot.Date), slot.TimeDisplay),
				Description: fmt.Sprintf("Dr. %s", slot.DoctorName),
			})
		}
		rows = append(rows, sm.ListRow{
			ID:          "more_slots",
			Title:       "Ver más horarios",
			Description: "Buscar horarios adicionales",
		})

		return sm.NewResult(sm.StateShowSlots).
			WithContext("available_slots_json", string(slotsJSON)).
			WithList(
				fmt.Sprintf("Horarios disponibles para *%s*:\n\nSelecciona el que prefieras:", cupsName),
				"Ver horarios",
				sm.ListSection{Title: "Horarios", Rows: rows},
			).
			WithEvent("slots_found", map[string]interface{}{"count": len(slots)}), nil
	}
}

// SHOW_SLOTS (interactivo) — usuario selecciona un slot de la lista.
func showSlotsHandler(addrMapper *services.AddressMapper) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		payload := strings.TrimSpace(msg.Text)
		if msg.PostbackPayload != "" {
			payload = msg.PostbackPayload
		}

		// "Ver más" — pagination
		if payload == "more_slots" {
			var slots []services.AvailableSlot
			json.Unmarshal([]byte(sess.GetContext("available_slots_json")), &slots)
			if len(slots) > 0 {
				lastSlot := slots[len(slots)-1]
				return sm.NewResult(sm.StateSearchSlots).
					WithContext("slots_after_date", lastSlot.Date).
					WithClearCtx("available_slots_json").
					WithEvent("more_slots_requested", nil), nil
			}
			return sm.NewResult(sm.StateSearchSlots).
				WithClearCtx("available_slots_json"), nil
		}

		// Validate selection against available slots
		var slots []services.AvailableSlot
		json.Unmarshal([]byte(sess.GetContext("available_slots_json")), &slots)

		var selected *services.AvailableSlot
		for _, s := range slots {
			if s.TimeSlot == payload {
				sel := s
				selected = &sel
				break
			}
		}

		if selected == nil {
			retryResult := sm.RetryOrEscalate(sess, "Selecciona un horario de la lista.")
			if retryResult.NextState == sm.StateEscalateToAgent {
				return retryResult, nil
			}
			// Invalid selection → re-show list
			cupsName := sess.GetContext("cups_name")
			rows := make([]sm.ListRow, 0, len(slots)+1)
			for _, slot := range slots {
				rows = append(rows, sm.ListRow{
					ID:          slot.TimeSlot,
					Title:       fmt.Sprintf("%s - %s", utils.FormatFriendlyDateShortStr(slot.Date), slot.TimeDisplay),
					Description: fmt.Sprintf("Dr. %s", slot.DoctorName),
				})
			}
			rows = append(rows, sm.ListRow{
				ID:          "more_slots",
				Title:       "Ver más horarios",
				Description: "Buscar horarios adicionales",
			})

			return sm.NewResult(sess.CurrentState).
				WithList(
					fmt.Sprintf("Selecciona un horario para *%s*:", cupsName),
					"Ver horarios",
					sm.ListSection{Title: "Horarios", Rows: rows},
				), nil
		}

		// Valid selection → show confirmation
		dateDisplay := selected.Date
		if dt, err := time.Parse("2006-01-02", selected.Date); err == nil {
			dateDisplay = utils.FormatFriendlyDate(dt)
		}

		summary := fmt.Sprintf("*Resumen de tu cita:*\n\n"+
			"Procedimiento: %s\n"+
			"Doctor: Dr. %s\n"+
			"Fecha: %s\n"+
			"Hora: %s",
			sess.GetContext("cups_name"),
			selected.DoctorName,
			dateDisplay,
			selected.TimeDisplay)

		if selected.ClinicAddress != "" {
			if addrMapper != nil {
				summary += "\n" + addrMapper.FormatAddress(selected.ClinicAddress)
			} else {
				summary += fmt.Sprintf("\nDirección: %s", selected.ClinicAddress)
			}
		}
		summary += "\n\n¿Confirmas esta cita?"

		return sm.NewResult(sm.StateConfirmBooking).
			WithContext("selected_slot_id", payload).
			WithButtons(summary,
				sm.Button{Text: "Confirmar cita", Payload: "booking_confirm"},
				sm.Button{Text: "Elegir otro", Payload: "booking_change"},
			).
			WithEvent("slot_selected", map[string]interface{}{"time_slot": payload}), nil
	}
}

// NO_SLOTS_AVAILABLE (automático) — no hay slots, ofrecer lista de espera.
// Cambio 12: reschedule_skip_cancel=="1" (admin cancellation) → auto-add to WL.
// Cambio 12b: reschedule_appt_id set + skip_cancel=="0" → appointment still active, no WL.
func noSlotsHandler(wlRepo WaitingListCreator) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		cupsName := sess.GetContext("cups_name")

		// Cambio 12: Auto-add to WL when coming from admin cancellation reschedule
		if sess.GetContext("reschedule_skip_cancel") == "1" && wlRepo != nil {
			return autoAddToWaitingList(ctx, sess, wlRepo, cupsName)
		}

		// Cambio 12b: Self-reschedule from active appointment (confirmation/reschedule template).
		// The old appointment is still active → don't offer WL (patient still has their slot).
		if sess.GetContext("reschedule_appt_id") != "" {
			r := sm.NewResult(sm.StatePostActionMenu).
				WithText("No hay horarios disponibles para *"+cupsName+"* en otra fecha.\n\n"+
					"Tu cita original sigue vigente.").
				WithEvent("no_slots_reschedule_active", map[string]interface{}{
					"cups_code":          sess.GetContext("cups_code"),
					"reschedule_appt_id": sess.GetContext("reschedule_appt_id"),
				})
			r.Messages = append(r.Messages, buildPostActionList("¿Qué deseas hacer ahora?"))
			return r, nil
		}

		return sm.NewResult(sm.StateOfferWaitingList).
			WithButtons(
				fmt.Sprintf("No hay horarios disponibles para *%s*.\n\n¿Deseas que te avisemos cuando haya disponibilidad?", cupsName),
				sm.Button{Text: "Sí, avisarme", Payload: "wl_yes"},
				sm.Button{Text: "No, gracias", Payload: "wl_no"},
			).
			WithEvent("no_slots_available", map[string]interface{}{"cups_code": sess.GetContext("cups_code")}), nil
	}
}

// autoAddToWaitingList adds the patient to the waiting list without asking (cancellation flow).
func autoAddToWaitingList(ctx context.Context, sess *session.Session, wlRepo WaitingListCreator, cupsName string) (*sm.StateResult, error) {
	patientID := sess.GetContext("patient_id")
	cupsCode := sess.GetContext("cups_code")

	// Check for duplicate
	hasActive, err := wlRepo.HasActiveForPatientAndCups(ctx, patientID, cupsCode)
	if err == nil && hasActive {
		dupMsg := "No hay horarios disponibles para *" + cupsName + "*.\n\n" +
			"Ya tienes una inscripcion activa en la lista de espera. " +
			"Te avisaremos por WhatsApp cuando haya disponibilidad."
		if next := advanceToNextProcedure(sess); next != nil {
			next.Messages = append([]sm.OutboundMessage{&sm.TextMessage{Text: dupMsg}}, next.Messages...)
			return next.WithEvent("waiting_list_auto_duplicate", map[string]interface{}{
				"cups_code":      cupsCode,
				"patient_id":     patientID,
				"next_procedure": true,
			}), nil
		}
		r := sm.NewResult(sm.StatePostActionMenu).
			WithText(dupMsg)
		r.Messages = append(r.Messages, buildPostActionList("¿Qué más deseas hacer?"))
		return r.WithEvent("waiting_list_auto_duplicate", map[string]interface{}{
			"cups_code":  cupsCode,
			"patient_id": patientID,
		}), nil
	}

	age, _ := strconv.Atoi(sess.GetContext("patient_age"))
	espacios, _ := strconv.Atoi(sess.GetContext("espacios"))
	if espacios == 0 {
		espacios = 1
	}

	entry := &domain.WaitingListEntry{
		ID:            uuid.New().String(),
		PhoneNumber:   sess.PhoneNumber,
		PatientID:     patientID,
		PatientDoc:    sess.GetContext("patient_doc"),
		PatientName:   sess.GetContext("patient_name"),
		PatientAge:    age,
		PatientGender: sess.GetContext("patient_gender"),
		PatientEntity: sess.GetContext("patient_entity"),
		CupsCode:      cupsCode,
		CupsName:      cupsName,
		IsContrasted:  sess.GetContext("is_contrasted") == "1",
		IsSedated:     sess.GetContext("is_sedated") == "1",
		Espacios:      espacios,
		ProceduresJSON: sess.GetContext("procedures_json"),
		ProcedureType:  sess.GetContext("procedure_type"),
		Status:        "waiting",
		ExpiresAt:     time.Now().AddDate(0, 0, 30),
	}

	entry.PreferredDoctorDoc = sess.GetContext("preferred_doctor_doc")

	if err := wlRepo.Create(ctx, entry); err != nil {
		slog.Error("auto_add_wl: create entry", "error", err)
		r := sm.NewResult(sm.StatePostActionMenu).
			WithText("No hay horarios disponibles para *" + cupsName + "*.\n\n" +
				"Ocurrio un error al inscribirte en la lista de espera. Intenta mas tarde.")
		r.Messages = append(r.Messages, buildPostActionList("¿Qué deseas hacer?"))
		return r.WithEvent("waiting_list_auto_failed", map[string]interface{}{
			"error": err.Error(),
		}), nil
	}

	autoMsg := "No hay horarios disponibles para *" + cupsName + "*.\n\n" +
		"Te hemos inscrito automaticamente en la *lista de espera*.\n" +
		"Te avisaremos por WhatsApp cuando haya disponibilidad.\n\n" +
		"La inscripcion es valida por 30 dias."

	if next := advanceToNextProcedure(sess); next != nil {
		next.Messages = append([]sm.OutboundMessage{&sm.TextMessage{Text: autoMsg}}, next.Messages...)
		return next.WithContext("waiting_list_entry_id", entry.ID).
			WithEvent("waiting_list_auto_joined", map[string]interface{}{
				"cups_code":      cupsCode,
				"patient_id":     patientID,
				"entry_id":       entry.ID,
				"next_procedure": true,
			}), nil
	}

	r := sm.NewResult(sm.StatePostActionMenu).
		WithText(autoMsg).
		WithContext("waiting_list_entry_id", entry.ID)
	r.Messages = append(r.Messages, buildPostActionList("¿Qué más deseas hacer?"))
	return r.WithEvent("waiting_list_auto_joined", map[string]interface{}{
		"cups_code":  cupsCode,
		"patient_id": patientID,
		"entry_id":   entry.ID,
	}), nil
}

// OFFER_WAITING_LIST (interactivo) — usuario decide unirse o no a la lista de espera.
func offerWaitingListHandler(wlRepo WaitingListCreator) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "wl_yes", "wl_no")
		if result != nil {
			cupsName := sess.GetContext("cups_name")
			result.Messages = append(result.Messages, &sm.ButtonMessage{
				Text: fmt.Sprintf("No hay horarios disponibles para *%s*.\n\n¿Deseas que te avisemos cuando haya disponibilidad?", cupsName),
				Buttons: []sm.Button{
					{Text: "Sí, avisarme", Payload: "wl_yes"},
					{Text: "No, gracias", Payload: "wl_no"},
				},
			})
			return result, nil
		}

		switch selected {
		case "wl_yes":
			patientID := sess.GetContext("patient_id")
			cupsCode := sess.GetContext("cups_code")
			cupsName := sess.GetContext("cups_name")

			// Verificar duplicado
			if wlRepo != nil {
				hasActive, err := wlRepo.HasActiveForPatientAndCups(ctx, patientID, cupsCode)
				if err == nil && hasActive {
					dupMsg := "Ya tienes una inscripcion activa en la lista de espera para *" + cupsName + "*.\nTe avisaremos cuando haya disponibilidad."
					if next := advanceToNextProcedure(sess); next != nil {
						next.Messages = append([]sm.OutboundMessage{&sm.TextMessage{Text: dupMsg}}, next.Messages...)
						return next.WithEvent("waiting_list_duplicate", map[string]interface{}{
							"cups_code":      cupsCode,
							"patient_id":     patientID,
							"next_procedure": true,
						}), nil
					}
					r := sm.NewResult(sm.StatePostActionMenu).
						WithText(dupMsg)
					r.Messages = append(r.Messages, buildPostActionList("¿Qué más deseas hacer?"))
					return r.WithEvent("waiting_list_duplicate", map[string]interface{}{
						"cups_code":  cupsCode,
						"patient_id": patientID,
					}), nil
				}
			}

			// Crear entry desde session context
			age, _ := strconv.Atoi(sess.GetContext("patient_age"))
			espacios, _ := strconv.Atoi(sess.GetContext("espacios"))
			if espacios == 0 {
				espacios = 1
			}

			entry := &domain.WaitingListEntry{
				ID:            uuid.New().String(),
				PhoneNumber:   sess.PhoneNumber,
				PatientID:     patientID,
				PatientDoc:    sess.GetContext("patient_doc"),
				PatientName:   sess.GetContext("patient_name"),
				PatientAge:    age,
				PatientGender: sess.GetContext("patient_gender"),
				PatientEntity: sess.GetContext("patient_entity"),
				CupsCode:      cupsCode,
				CupsName:      cupsName,
				IsContrasted:  sess.GetContext("is_contrasted") == "1",
				IsSedated:     sess.GetContext("is_sedated") == "1",
				Espacios:      espacios,
				ProceduresJSON: sess.GetContext("procedures_json"),
				ProcedureType:  sess.GetContext("procedure_type"),
				Status:        "waiting",
				ExpiresAt:     time.Now().AddDate(0, 0, 30),
			}

			// GFR data (si aplica)
			if gfr := sess.GetContext("gfr_creatinine"); gfr != "" {
				entry.GfrCreatinine, _ = strconv.ParseFloat(gfr, 64)
				entry.GfrHeightCm, _ = strconv.Atoi(sess.GetContext("gfr_height_cm"))
				entry.GfrWeightKg, _ = strconv.ParseFloat(sess.GetContext("gfr_weight_kg"), 64)
				entry.GfrDiseaseType = sess.GetContext("gfr_disease_type")
				entry.GfrCalculated, _ = strconv.ParseFloat(sess.GetContext("gfr_calculated"), 64)
			}

			// Extras
			entry.IsPregnant = sess.GetContext("is_pregnant") == "1"
			entry.BabyWeightCat = sess.GetContext("baby_weight_cat")
			entry.PreferredDoctorDoc = sess.GetContext("preferred_doctor_doc")

			// Guardar en BD
			if wlRepo != nil {
				if err := wlRepo.Create(ctx, entry); err != nil {
					r := sm.NewResult(sm.StatePostActionMenu).
						WithText("Ocurrio un error al inscribirte en la lista de espera. Intenta mas tarde.")
					r.Messages = append(r.Messages, buildPostActionList("¿Qué deseas hacer?"))
					return r.WithEvent("waiting_list_creation_failed", map[string]interface{}{
						"error": err.Error(),
					}), nil
				}
			}

			wlMsg := "Te hemos inscrito en la *lista de espera*.\n\n" +
				"Te enviaremos un mensaje de WhatsApp cuando haya disponibilidad para *" + cupsName + "*.\n\n" +
				"La inscripcion es valida por 30 dias."

			if next := advanceToNextProcedure(sess); next != nil {
				next.Messages = append([]sm.OutboundMessage{&sm.TextMessage{Text: wlMsg}}, next.Messages...)
				return next.WithContext("waiting_list_entry_id", entry.ID).
					WithEvent("waiting_list_joined", map[string]interface{}{
						"cups_code":      cupsCode,
						"patient_id":     patientID,
						"entry_id":       entry.ID,
						"next_procedure": true,
					}), nil
			}

			r := sm.NewResult(sm.StatePostActionMenu).
				WithText(wlMsg).
				WithContext("waiting_list_entry_id", entry.ID)
			r.Messages = append(r.Messages, buildPostActionList("¿Qué más deseas hacer?"))
			return r.WithEvent("waiting_list_joined", map[string]interface{}{
				"cups_code":  cupsCode,
				"patient_id": patientID,
				"entry_id":   entry.ID,
			}), nil

		case "wl_no":
			if next := advanceToNextProcedure(sess); next != nil {
				return next.WithEvent("waiting_list_declined", map[string]interface{}{
					"cups_code":      sess.GetContext("cups_code"),
					"next_procedure": true,
				}), nil
			}
			r := sm.NewResult(sm.StatePostActionMenu)
			r.Messages = append(r.Messages, buildPostActionList("¿Qué deseas hacer ahora?"))
			return r.WithEvent("waiting_list_declined", map[string]interface{}{
				"cups_code": sess.GetContext("cups_code"),
			}), nil
		}

		return nil, fmt.Errorf("unreachable")
	}
}

// CONFIRM_BOOKING — solo lógica de negocio (validación declarativa en RegisterWithConfig).
func confirmBookingHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		selected := sm.ValidatedPayload(ctx)

		switch selected {
		case "booking_confirm":
			return sm.NewResult(sm.StateReconfirmBooking).
				WithButtons("¿Estás seguro de *confirmar* esta cita?",
					sm.Button{Text: "Sí, confirmar", Payload: "reconfirm_yes"},
					sm.Button{Text: "No, volver", Payload: "reconfirm_no"},
				).
				WithEvent("booking_reconfirm_requested", nil), nil

		case "booking_change":
			return sm.NewResult(sm.StateSearchSlots).
				WithClearCtx("selected_slot_id", "available_slots_json", "slots_after_date").
				WithEvent("booking_change_requested", nil), nil
		}

		return nil, fmt.Errorf("unreachable")
	}
}

// RECONFIRM_BOOKING (interactivo) — segunda confirmación antes de crear la cita.
func reconfirmBookingHandler(addrMapper *services.AddressMapper) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		result, selected := sm.ValidateButtonResponse(sess, msg, "reconfirm_yes", "reconfirm_no")
		if result != nil {
			if result.NextState == sm.StateEscalateToAgent {
				return result, nil
			}
			result.Messages = nil
			return sm.NewResult(sess.CurrentState).
				WithButtons("¿Estás seguro de *confirmar* esta cita?",
					sm.Button{Text: "Sí, confirmar", Payload: "reconfirm_yes"},
					sm.Button{Text: "No, volver", Payload: "reconfirm_no"},
				), nil
		}

		switch selected {
		case "reconfirm_yes":
			return sm.NewResult(sm.StateCreateAppointment).
				WithEvent("booking_confirmed", nil), nil

		case "reconfirm_no":
			// Re-mostrar resumen de la cita
			slot := findSelectedSlot(sess)
			if slot == nil {
				return sm.NewResult(sm.StateSearchSlots).
					WithText("Slot no encontrado. Buscando nuevos horarios...").
					WithClearCtx("selected_slot_id", "available_slots_json"), nil
			}
			summary := buildBookingSummary(sess, slot, addrMapper)
			return sm.NewResult(sm.StateConfirmBooking).
				WithButtons(summary,
					sm.Button{Text: "Confirmar cita", Payload: "booking_confirm"},
					sm.Button{Text: "Elegir otro", Payload: "booking_change"},
				), nil
		}

		return nil, fmt.Errorf("unreachable: selected=%s", selected)
	}
}

// CREATE_APPOINTMENT (automático) — crea la cita en la BD externa.
func createAppointmentHandler(apptSvc *services.AppointmentService, soatRepo repository.SoatRepository) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		slot := findSelectedSlot(sess)
		if slot == nil {
			return sm.NewResult(sm.StateBookingFailed).
				WithContext("booking_failure_reason", "slot_not_found"), nil
		}

		// Build observations
		isContrasted := sess.GetContext("is_contrasted") == "1"
		isSedated := sess.GetContext("is_sedated") == "1"
		observations := buildObservations(isContrasted, isSedated)

		cupsCode := sess.GetContext("cups_code")
		entity := sess.GetContext("patient_entity")

		// Get SOAT price (best effort, defaults to 0)
		var unitValue float64
		if soatRepo != nil {
			price, _ := soatRepo.FindPrice(ctx, cupsCode, entity)
			unitValue = price
		}

		// Parse date
		date, _ := time.Parse("2006-01-02", slot.Date)

		input := domain.CreateAppointmentInput{
			Date:         date,
			TimeSlot:     slot.TimeSlot,
			DoctorID:     slot.DoctorDoc,
			PatientID:    sess.GetContext("patient_id"),
			Entity:       entity,
			AgendaID:     slot.AgendaID,
			CreatedBy:    "0", // Bot-created
			Observations: observations,
			Procedures: []domain.CreateProcedureInput{
				{
					CupCode:   cupsCode,
					Quantity:  1,
					UnitValue: unitValue,
				},
			},
		}

		espacios, _ := strconv.Atoi(sess.GetContext("espacios"))
		apptID, err := apptSvc.CreateWithConsecutive(ctx, input, espacios, slot.Duration)
		if err != nil {
			if strings.Contains(err.Error(), "slot_taken") {
				return sm.NewResult(sm.StateBookingFailed).
					WithContext("booking_failure_reason", "slot_taken"), nil
			}
			return sm.NewResult(sm.StateBookingFailed).
				WithContext("booking_failure_reason", "error").
				WithEvent("appointment_create_error", map[string]interface{}{"error": err.Error()}), nil
		}

		// Cancel old appointment if this is a self-service reschedule
		rescheduleApptID := sess.GetContext("reschedule_appt_id")
		if rescheduleApptID != "" && sess.GetContext("reschedule_skip_cancel") != "1" {
			_, oldBlock, findErr := apptSvc.FindBlockByAppointmentID(ctx, rescheduleApptID)
			if findErr != nil {
				slog.Error("reschedule: find old block", "error", findErr, "old_appt_id", rescheduleApptID)
			} else if len(oldBlock) > 0 {
				if cancelErr := apptSvc.CancelBlock(ctx, oldBlock, "reprogramada por paciente via bot", "whatsapp_bot", ""); cancelErr != nil {
					slog.Error("reschedule: cancel old block", "error", cancelErr, "old_appt_id", rescheduleApptID)
				} else {
					slog.Info("reschedule: old appointment cancelled",
						"old_appt_id", rescheduleApptID,
						"new_appt_id", apptID,
						"block_size", len(oldBlock))
				}
			}
		}

		return sm.NewResult(sm.StateBookingSuccess).
			WithContext("created_appointment_id", apptID).
			WithEvent("appointment_created", map[string]interface{}{
				"appointment_id":  apptID,
				"cups_code":       cupsCode,
				"date":            slot.Date,
				"time":            slot.TimeDisplay,
				"doctor":          slot.DoctorName,
				"espacios":        espacios,
				"reschedule_from": rescheduleApptID,
			}), nil
	}
}

// BOOKING_SUCCESS (automático) — cita creada exitosamente.
func bookingSuccessHandler(addrMapper *services.AddressMapper) sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		slot := findSelectedSlot(sess)
		cupsName := sess.GetContext("cups_name")

		var doctorName, dateDisplay, timeDisplay, address string
		if slot != nil {
			doctorName = slot.DoctorName
			timeDisplay = slot.TimeDisplay
			address = slot.ClinicAddress
			if dt, err := time.Parse("2006-01-02", slot.Date); err == nil {
				dateDisplay = utils.FormatFriendlyDate(dt)
			} else {
				dateDisplay = slot.Date
			}
		}

		header := "*Cita agendada exitosamente*"
		if sess.GetContext("reschedule_appt_id") != "" {
			header = "*Tu cita ha sido reprogramada exitosamente*"
		}

		successMsg := fmt.Sprintf("%s\n\n"+
			"Procedimiento: %s\n"+
			"Doctor: Dr. %s\n"+
			"Fecha: %s\n"+
			"Hora: %s",
			header, cupsName, doctorName, dateDisplay, timeDisplay)

		if address != "" {
			if addrMapper != nil {
				successMsg += "\n" + addrMapper.FormatAddress(address)
			} else {
				successMsg += fmt.Sprintf("\nDirección: %s", address)
			}
		}

		// Preparation instructions
		if prep := sess.GetContext("cups_preparation"); prep != "" {
			successMsg += fmt.Sprintf("\n\n📋 *Preparación:*\n%s", prep)
		}
		if videoURL := sess.GetContext("cups_video_url"); videoURL != "" {
			successMsg += fmt.Sprintf("\n\n🎥 *Video de preparación:*\n%s", videoURL)
		}
		if audioURL := sess.GetContext("cups_audio_url"); audioURL != "" {
			successMsg += fmt.Sprintf("\n\n🎵 *Audio:*\n%s", audioURL)
		}

		successMsg += "\n\nRecuerda presentarte 15 minutos antes con tu documento y orden médica."

		// Check multi-procedure flow
		if next := advanceToNextProcedure(sess); next != nil {
			next.Messages = append([]sm.OutboundMessage{&sm.TextMessage{Text: successMsg}}, next.Messages...)
			return next.WithEvent("booking_success", map[string]interface{}{
				"appointment_id": sess.GetContext("created_appointment_id"),
				"cups_code":      sess.GetContext("cups_code"),
				"next_procedure": true,
			}), nil
		}

		// No more procedures → post action menu
		r := sm.NewResult(sm.StatePostActionMenu).
			WithText(successMsg)
		r.Messages = append(r.Messages, buildPostActionList("¿Qué más deseas hacer?"))
		return r.WithEvent("booking_success", map[string]interface{}{
			"appointment_id": sess.GetContext("created_appointment_id"),
			"cups_code":      sess.GetContext("cups_code"),
			"next_procedure": false,
		}), nil
	}
}

// BOOKING_FAILED (automático) — error al crear cita.
func bookingFailedHandler() sm.StateHandler {
	return func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*sm.StateResult, error) {
		reason := sess.GetContext("booking_failure_reason")

		switch reason {
		case "slot_taken":
			return sm.NewResult(sm.StateSearchSlots).
				WithText("El horario que seleccionaste ya fue tomado por otro paciente. Buscando nuevos horarios...").
				WithClearCtx("selected_slot_id", "available_slots_json").
				WithEvent("slot_taken", nil), nil

		default:
			r := sm.NewResult(sm.StatePostActionMenu).
				WithText("Ocurrió un error al crear la cita. Por favor intenta más tarde.")
			r.Messages = append(r.Messages, buildPostActionList("¿Qué deseas hacer?"))
			return r.WithEvent("booking_failed", map[string]interface{}{"reason": reason}), nil
		}
	}
}

// --- Helpers ---

// findSelectedSlot retrieves the selected slot from session context.
func findSelectedSlot(sess *session.Session) *services.AvailableSlot {
	selectedSlotID := sess.GetContext("selected_slot_id")
	var slots []services.AvailableSlot
	json.Unmarshal([]byte(sess.GetContext("available_slots_json")), &slots)

	for _, s := range slots {
		if s.TimeSlot == selectedSlotID {
			return &s
		}
	}
	return nil
}

// buildBookingSummary creates the booking confirmation text.
func buildBookingSummary(sess *session.Session, slot *services.AvailableSlot, addrMapper *services.AddressMapper) string {
	dateDisplay := slot.Date
	if dt, err := time.Parse("2006-01-02", slot.Date); err == nil {
		dateDisplay = utils.FormatFriendlyDate(dt)
	}

	summary := fmt.Sprintf("*Resumen de tu cita:*\n\n"+
		"Procedimiento: %s\n"+
		"Doctor: Dr. %s\n"+
		"Fecha: %s\n"+
		"Hora: %s",
		sess.GetContext("cups_name"),
		slot.DoctorName,
		dateDisplay,
		slot.TimeDisplay)

	if slot.ClinicAddress != "" {
		if addrMapper != nil {
			summary += "\n" + addrMapper.FormatAddress(slot.ClinicAddress)
		} else {
			summary += fmt.Sprintf("\nDirección: %s", slot.ClinicAddress)
		}
	}

	if prep := sess.GetContext("cups_preparation"); prep != "" {
		summary += fmt.Sprintf("\n\n📋 *Preparación:*\n%s", prep)
	}
	if videoURL := sess.GetContext("cups_video_url"); videoURL != "" {
		summary += fmt.Sprintf("\n\n🎥 *Video:* %s", videoURL)
	}
	if audioURL := sess.GetContext("cups_audio_url"); audioURL != "" {
		summary += fmt.Sprintf("\n\n🎵 *Audio:* %s", audioURL)
	}

	summary += "\n\n¿Confirmas esta cita?"

	return summary
}

// buildObservations creates the observations string for the appointment.
func buildObservations(isContrasted, isSedated bool) string {
	var parts []string
	if isContrasted {
		parts = append(parts, "Contrastada")
	}
	if isSedated {
		parts = append(parts, "Bajo Sedacion")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}
