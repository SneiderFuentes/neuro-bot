package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/config"
	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/notifications"
	"github.com/neuro-bot/neuro-bot/internal/repository"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/utils"
)

// WaitingListRepo interface for waiting list operations in scheduler.
type WaitingListRepo interface {
	ExpireOld(ctx context.Context, days int) (int64, error)
	GetDistinctWaitingCups(ctx context.Context) ([]string, error)
	GetWaitingByCups(ctx context.Context, cupsCode string, limit int) ([]domain.WaitingListEntry, error)
	UpdateStatus(ctx context.Context, id, status string) error
	MarkNotified(ctx context.Context, id string) error
}

// EventLogger logs events for auditing (matches tracking.EventTracker).
type EventLogger interface {
	LogEvent(ctx context.Context, sessionID, phone, eventType string, data map[string]interface{})
}

// Tasks holds dependencies for all scheduler tasks.
type Tasks struct {
	AppointmentRepo repository.AppointmentRepository
	BirdClient      *bird.Client
	NotifyManager   *notifications.NotificationManager
	WaitingListRepo WaitingListRepo
	SlotService     *services.SlotService
	Cfg             *config.Config
	Tracker         EventLogger
}

// RegisterAll registers the 4 scheduled tasks.
func (t *Tasks) RegisterAll(s *Scheduler) {
	// 02:00 — Data cleanup
	s.AddTask(ScheduledTask{
		Name: "data_cleanup",
		Hour: 2, Minute: 0,
		Fn: t.cleanup,
	})

	// 07:00 — WhatsApp reminders for tomorrow's appointments
	s.AddTask(ScheduledTask{
		Name: "whatsapp_reminders",
		Hour: 7, Minute: 0,
		Weekdays: []time.Weekday{
			time.Monday, time.Tuesday, time.Wednesday,
			time.Thursday, time.Friday, time.Saturday,
		},
		Fn: t.sendWhatsAppReminders,
	})

	// 08:00 — Waiting list check (Phase 13)
	s.AddTask(ScheduledTask{
		Name: "waiting_list_check",
		Hour: 8, Minute: 0,
		Weekdays: []time.Weekday{
			time.Monday, time.Tuesday, time.Wednesday,
			time.Thursday, time.Friday,
		},
		Fn: t.checkWaitingList,
	})

	// 15:00 — IVR calls for non-responders
	s.AddTask(ScheduledTask{
		Name: "voice_reminders",
		Hour: 15, Minute: 0,
		Weekdays: []time.Weekday{
			time.Monday, time.Tuesday, time.Wednesday,
			time.Thursday, time.Friday, time.Saturday,
		},
		Fn: t.sendVoiceReminders,
	})
}

// === Task 07:00: WhatsApp Reminders ===

func (t *Tasks) sendWhatsAppReminders(ctx context.Context) error {
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	appointments, err := t.AppointmentRepo.FindPendingByDate(ctx, tomorrow)
	if err != nil {
		return fmt.Errorf("fetch tomorrow appointments: %w", err)
	}

	slog.Info("whatsapp reminders", "date", tomorrow, "appointments", len(appointments))
	if len(appointments) == 0 {
		return nil
	}

	// Group by patient
	patientGroups := groupAppointmentsByPatient(appointments)

	sent := 0
	skipped := 0

	for _, group := range patientGroups {
		firstAppt := group[0]

		phone := utils.ParseColombianPhone(firstAppt.PatientPhone)
		if phone == "" {
			skipped++
			slog.Warn("skipping reminder - invalid phone",
				"patient_id", firstAppt.PatientID,
				"phone", firstAppt.PatientPhone)
			continue
		}

		// Build procedure names
		var procedures []string
		for _, appt := range group {
			cupName := services.GetFirstCupName(appt)
			procedures = append(procedures, cupName)
		}
		proceduresText := strings.Join(procedures, " y ")

		appointmentDate := utils.FormatFriendlyDate(firstAppt.Date)
		appointmentTime := services.FormatTimeSlot(firstAppt.TimeSlot)

		// Send confirmation template
		tmpl := bird.TemplateConfig{
			ProjectID: t.Cfg.BirdTemplateConfirmProjectID,
			VersionID: t.Cfg.BirdTemplateConfirmVersionID,
			Locale:    t.Cfg.BirdTemplateConfirmLocale,
			Params: []bird.TemplateParam{
				{Type: "string", Key: "patient_name", Value: firstAppt.PatientName},
				{Type: "string", Key: "clinic_name", Value: t.Cfg.CenterName},
				{Type: "string", Key: "appointment_date", Value: appointmentDate},
				{Type: "string", Key: "appointment_time", Value: appointmentTime},
				{Type: "string", Key: "procedures", Value: proceduresText},
			},
		}

		msgID, err := t.BirdClient.SendTemplate(phone, tmpl)
		if err != nil {
			slog.Error("send reminder failed", "phone", phone, "error", err)
			continue
		}

		// Try to get conversationID for Bird Inbox visibility
		convID := t.BirdClient.GetCachedConversationID(phone)
		if convID == "" {
			convID, _ = t.BirdClient.LookupConversationByPhone(phone)
		}

		// Register pending notification with 6h timer
		t.NotifyManager.RegisterPending(notifications.PendingNotification{
			Type:           "confirmation",
			Phone:          phone,
			AppointmentID:  firstAppt.ID,
			BirdMessageID:  msgID,
			ConversationID: convID,
		})

		// Log event
		if t.Tracker != nil {
			t.Tracker.LogEvent(ctx, "", phone, "notification_sent",
				map[string]interface{}{
					"type":            "confirmation",
					"appointment_id":  firstAppt.ID,
					"bird_msg_id":     msgID,
					"conversation_id": convID,
				})
		}

		sent++

		// Rate limiting: 2 seconds between sends
		time.Sleep(2 * time.Second)
	}

	slog.Info("whatsapp reminders complete", "sent", sent, "skipped", skipped)
	return nil
}

// === Task 15:00: Voice Reminders (IVR) ===

func (t *Tasks) sendVoiceReminders(ctx context.Context) error {
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	// Find appointments for tomorrow where patient hasn't responded to WhatsApp
	appointments, err := t.AppointmentRepo.FindPendingByDate(ctx, tomorrow)
	if err != nil {
		return fmt.Errorf("fetch non-responders: %w", err)
	}

	// Filter to only those who didn't respond (still pending in notification manager)
	var nonResponders []domain.Appointment
	seen := make(map[string]bool)
	for _, appt := range appointments {
		phone := utils.ParseColombianPhone(appt.PatientPhone)
		if phone == "" || seen[phone] {
			continue
		}
		// Only call if they have no pending notification (meaning WhatsApp timed out)
		if !t.NotifyManager.HasPending(phone) && !appt.Confirmed {
			nonResponders = append(nonResponders, appt)
			seen[phone] = true
		}
	}

	slog.Info("voice reminders", "non_responders", len(nonResponders))

	for _, appt := range nonResponders {
		phone := utils.ParseColombianPhone(appt.PatientPhone)
		if phone == "" {
			continue
		}

		_, err := t.BirdClient.PlaceCall(phone, map[string]string{
			"patient_name":     appt.PatientName,
			"appointment_date": utils.FormatFriendlyDate(appt.Date),
			"appointment_time": services.FormatTimeSlot(appt.TimeSlot),
		})
		if err != nil {
			slog.Error("voice call failed", "phone", phone, "error", err)
		}

		if t.Tracker != nil {
			t.Tracker.LogEvent(ctx, "", phone, "notification_ivr_sent",
				map[string]interface{}{"appointment_id": appt.ID})
		}

		slog.Info("ivr call initiated", "phone", phone, "appointment_id", appt.ID)

		time.Sleep(3 * time.Second) // Rate limit for calls
	}

	return nil
}

// === Task 02:00: Cleanup ===

func (t *Tasks) cleanup(ctx context.Context) error {
	// Note: session cleanup is handled by StartInactivityChecker (Fase 20)

	// Expire old waiting list entries
	if t.WaitingListRepo != nil {
		wlExpired, err := t.WaitingListRepo.ExpireOld(ctx, 30)
		if err != nil {
			slog.Error("waiting list cleanup error", "error", err)
		} else if wlExpired > 0 {
			slog.Info("waiting list entries expired by cleanup", "count", wlExpired)
		}
	}

	return nil
}

// === Task 08:00: Waiting List Check ===

func (t *Tasks) checkWaitingList(ctx context.Context) error {
	if t.WaitingListRepo == nil || t.SlotService == nil {
		slog.Debug("waiting list check: dependencies not available")
		return nil
	}

	// 1. Expirar entries > 30 días
	expired, err := t.WaitingListRepo.ExpireOld(ctx, 30)
	if err != nil {
		slog.Error("expire waiting list", "error", err)
	}
	if expired > 0 {
		slog.Info("waiting list entries expired", "count", expired)
	}

	// 2. Obtener CUPS distintos con entries en estado "waiting"
	cupsCodes, err := t.WaitingListRepo.GetDistinctWaitingCups(ctx)
	if err != nil {
		return fmt.Errorf("get waiting cups: %w", err)
	}

	if len(cupsCodes) == 0 {
		slog.Debug("waiting list check: no waiting entries")
		return nil
	}

	totalNotified := 0

	for _, cupsCode := range cupsCodes {
		// 3. Buscar slots disponibles (usar primera entry como referencia)
		entries, err := t.WaitingListRepo.GetWaitingByCups(ctx, cupsCode, 1)
		if err != nil || len(entries) == 0 {
			continue
		}

		firstEntry := entries[0]

		query := services.SlotQuery{
			CupsCode:     cupsCode,
			PatientAge:   firstEntry.PatientAge,
			IsContrasted: firstEntry.IsContrasted,
			IsSedated:    firstEntry.IsSedated,
			Espacios:     firstEntry.Espacios,
			MaxSlots:     20, // Buscar más slots para saber cuántos pacientes notificar
		}

		slots, err := t.SlotService.GetAvailableSlots(ctx, query)
		if err != nil || len(slots) == 0 {
			slog.Debug("waiting list: no slots for cups", "cups_code", cupsCode)
			continue
		}

		slog.Info("waiting list: slots found", "cups_code", cupsCode, "slots", len(slots))

		// 4. Obtener primeras N entries FIFO (N = slots disponibles)
		entriesToNotify, err := t.WaitingListRepo.GetWaitingByCups(ctx, cupsCode, len(slots))
		if err != nil {
			continue
		}

		for _, entry := range entriesToNotify {
			// 5. Verificar si ya tiene cita para este CUPS
			hasFuture, err := t.AppointmentRepo.HasFutureForCup(ctx, entry.PatientID, cupsCode)
			if err != nil {
				continue
			}
			if hasFuture {
				t.WaitingListRepo.UpdateStatus(ctx, entry.ID, "duplicate_found")
				slog.Info("waiting list: duplicate found", "entry_id", entry.ID, "cups_code", cupsCode)
				continue
			}

			// 6. Enviar template de disponibilidad
			if t.Cfg.BirdTemplateWaitingListProjectID == "" {
				continue
			}

			tmpl := bird.TemplateConfig{
				ProjectID: t.Cfg.BirdTemplateWaitingListProjectID,
				VersionID: t.Cfg.BirdTemplateWaitingListVersionID,
				Locale:    t.Cfg.BirdTemplateWaitingListLocale,
				Params: []bird.TemplateParam{
					{Type: "string", Key: "patient_name", Value: entry.PatientName},
					{Type: "string", Key: "procedure_name", Value: entry.CupsName},
					{Type: "string", Key: "cups_code", Value: entry.CupsCode},
				},
			}

			msgID, err := t.BirdClient.SendTemplate(entry.PhoneNumber, tmpl)
			if err != nil {
				slog.Error("send waiting list notification", "phone", entry.PhoneNumber, "error", err)
				continue
			}

			// 7. Marcar como notified
			t.WaitingListRepo.MarkNotified(ctx, entry.ID)

			// Try to get conversationID for Bird Inbox visibility
			wlConvID := t.BirdClient.GetCachedConversationID(entry.PhoneNumber)
			if wlConvID == "" {
				wlConvID, _ = t.BirdClient.LookupConversationByPhone(entry.PhoneNumber)
			}

			// 8. Registrar pending notification con timer 6h
			t.NotifyManager.RegisterPending(notifications.PendingNotification{
				Type:           "waiting_list",
				Phone:          entry.PhoneNumber,
				WaitingListID:  entry.ID,
				BirdMessageID:  msgID,
				ConversationID: wlConvID,
			})

			if t.Tracker != nil {
				t.Tracker.LogEvent(ctx, "", entry.PhoneNumber, "notification_sent",
					map[string]interface{}{
						"type":             "waiting_list",
						"waiting_list_id":  entry.ID,
						"bird_msg_id":      msgID,
						"conversation_id":  wlConvID,
					})
			}

			totalNotified++
			slog.Info("waiting list notification sent", "phone", entry.PhoneNumber, "entry_id", entry.ID)

			time.Sleep(2 * time.Second) // Rate limit
		}
	}

	slog.Info("waiting list check complete", "cups_checked", len(cupsCodes), "notified", totalNotified)
	return nil
}

// === Helpers ===

func groupAppointmentsByPatient(appointments []domain.Appointment) map[string][]domain.Appointment {
	groups := make(map[string][]domain.Appointment)
	for _, appt := range appointments {
		groups[appt.PatientID] = append(groups[appt.PatientID], appt)
	}
	return groups
}
