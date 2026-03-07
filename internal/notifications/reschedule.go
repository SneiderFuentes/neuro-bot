package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/utils"
)

// handleReschedule processes responses to the reschedule notification template.
// Postback "confirm" → confirms the new (rescheduled) appointment.
// Postback "cancel" → cancels the rescheduled appointment.
func (m *NotificationManager) handleReschedule(phone, action string, pending *PendingNotification) {
	ctx := context.Background()

	switch action {
	case "confirm":
		appt, block, err := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)
		if err != nil || appt == nil {
			slog.Error("reschedule confirm: find appointment", "error", err, "appointment_id", pending.AppointmentID)
			return
		}

		m.apptSvc.ConfirmBlock(ctx, block, "whatsapp_bot", pending.ConversationID)

		// Build procedure names
		var procNames []string
		for _, p := range appt.Procedures {
			if p.CupName != "" {
				procNames = append(procNames, p.CupName)
			}
		}
		proceduresText := strings.Join(procNames, " y ")
		if proceduresText == "" {
			proceduresText = "Procedimiento"
		}

		// Build confirmation message with details
		msg := fmt.Sprintf("Tu cita reprogramada ha sido confirmada!\n\n"+
			"*Fecha:* %s\n"+
			"*Hora:* %s\n"+
			"*Medico:* %s\n"+
			"*Procedimiento:* %s",
			utils.FormatFriendlyDate(appt.Date),
			services.FormatTimeSlot(appt.TimeSlot),
			appt.DoctorName,
			proceduresText,
		)

		// Look up address and preparations from procedure DB
		if m.procRepo != nil && len(appt.Procedures) > 0 {
			var prepText string
			address := ""

			for _, proc := range appt.Procedures {
				if proc.CupCode == "" {
					continue
				}
				p, err := m.procRepo.FindByCode(ctx, proc.CupCode)
				if err != nil || p == nil {
					continue
				}
				if address == "" && p.Address != "" {
					address = p.Address
				}
				if p.Preparation != "" {
					prepText += fmt.Sprintf("\n- Para *%s*: %s", proc.CupName, p.Preparation)
					if p.VideoURL != "" {
						prepText += fmt.Sprintf("\n  Video: %s", p.VideoURL)
					}
				}
			}

			if address != "" {
				msg += fmt.Sprintf("\n*Direccion:* %s", address)
			}
			if prepText != "" {
				msg += "\n\n*Preparacion:*" + prepText
			}
		}

		msg += "\n\nRecuerda presentarte 15 minutos antes. Te esperamos!"

		m.birdClient.SendText(phone, pending.ConversationID, msg)

		if pending.ConversationID != "" {
			m.birdClient.UpdateFeedItem(pending.ConversationID, pending.BirdMessageID, true, "", "")
		}

		if m.tracker != nil {
			elapsed := time.Since(pending.CreatedAt).Minutes()
			m.tracker.LogEvent(ctx, "", phone, "notification_reschedule_confirmed", map[string]interface{}{
				"appointment_id":    pending.AppointmentID,
				"response_time_min": int(elapsed),
				"conversation_id":   pending.ConversationID,
			})
		}

		slog.Info("reschedule confirm success", "phone", phone, "appointment_id", pending.AppointmentID)

	case "reschedule":
		m.startSelfReschedule(phone, pending, false)

	case "cancel":
		appt, block, err := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)
		if err != nil || block == nil {
			slog.Error("reschedule cancel: find appointment", "error", err, "appointment_id", pending.AppointmentID)
			return
		}

		m.apptSvc.CancelBlock(ctx, block, "paciente rechaza reprogramacion", "whatsapp_bot", pending.ConversationID)

		m.birdClient.SendText(phone, pending.ConversationID, "Tu cita reprogramada ha sido cancelada.\n\n"+
			"Si necesitas agendar una nueva cita, escribenos.")

		if pending.ConversationID != "" {
			m.birdClient.UpdateFeedItem(pending.ConversationID, pending.BirdMessageID,
				false, m.cfg.BirdTeamFallback, "")
		}

		// Cambio 13: Check waiting list for freed CUPS
		if appt != nil {
			for _, proc := range appt.Procedures {
				if proc.CupCode != "" {
					go m.CheckWaitingListForCups(ctx, proc.CupCode)
				}
			}
		}

		if m.tracker != nil {
			elapsed := time.Since(pending.CreatedAt).Minutes()
			m.tracker.LogEvent(ctx, "", phone, "notification_reschedule_cancelled", map[string]interface{}{
				"appointment_id":    pending.AppointmentID,
				"response_time_min": int(elapsed),
				"conversation_id":   pending.ConversationID,
			})
		}

		slog.Info("reschedule cancel success", "phone", phone, "appointment_id", pending.AppointmentID)
	}
}

// handleCancellation processes responses to the agenda cancellation template.
// NOTE: Caller (HandleResponse) already removed pending from sync.Map via LoadAndDelete.
func (m *NotificationManager) handleCancellation(phone, action string, pending *PendingNotification) {
	ctx := context.Background()

	switch action {
	case "acknowledge": // postback: "understood"
		m.birdClient.SendText(phone, pending.ConversationID,
			"Entendido. Lamentamos los inconvenientes. "+
				"Si necesitas reagendar tu cita, puedes escribirnos cuando gustes.")

		if pending.ConversationID != "" {
			m.birdClient.UpdateFeedItem(pending.ConversationID, pending.BirdMessageID, true, "", "")
		}

		if m.tracker != nil {
			elapsed := time.Since(pending.CreatedAt).Minutes()
			m.tracker.LogEvent(ctx, "", phone, "notification_cancel_acknowledged", map[string]interface{}{
				"appointment_id":    pending.AppointmentID,
				"response_time_min": int(elapsed),
				"conversation_id":   pending.ConversationID,
			})
		}

		slog.Info("cancellation acknowledged", "phone", phone, "appointment_id", pending.AppointmentID)

	case "reschedule": // postback: "reprogramar" or "reschedule"
		m.startSelfReschedule(phone, pending, true) // skipCancel=true: already cancelled by admin
	}
}
