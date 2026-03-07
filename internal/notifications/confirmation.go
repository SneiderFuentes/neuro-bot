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

// handleConfirmation processes responses to the daily confirmation template.
// NOTE: Caller (HandleResponse) already removed pending from sync.Map and DB via LoadAndDelete.
func (m *NotificationManager) handleConfirmation(phone, action string, pending *PendingNotification) {
	ctx := context.Background()

	switch action {
	case "confirm":
		appt, block, err := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)
		if err != nil || appt == nil {
			slog.Error("confirm: find appointment", "error", err, "appointment_id", pending.AppointmentID)
			return
		}

		if err := m.apptSvc.ConfirmBlock(ctx, block, "whatsapp_bot", pending.ConversationID); err != nil {
			slog.Error("confirm: confirm block", "error", err)
		}

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
		msg := fmt.Sprintf("Tu cita ha sido confirmada!\n\n"+
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

		// Log event
		if m.tracker != nil {
			elapsed := time.Since(pending.CreatedAt).Minutes()
			m.tracker.LogEvent(ctx, "", phone, "notification_confirmed",
				map[string]interface{}{
					"appointment_id":    pending.AppointmentID,
					"response_time_min": int(elapsed),
					"conversation_id":   pending.ConversationID,
				})
		}

		slog.Info("proactive confirmation success",
			"phone", phone,
			"appointment_id", pending.AppointmentID,
			"block_size", len(block))

	case "reschedule":
		m.startSelfReschedule(phone, pending, false)

	case "cancel":
		appt, block, err := m.apptSvc.FindBlockByAppointmentID(ctx, pending.AppointmentID)
		if err != nil || appt == nil {
			slog.Error("cancel: find appointment", "error", err, "appointment_id", pending.AppointmentID)
			return
		}

		if err := m.apptSvc.CancelBlock(ctx, block, "paciente via bot", "whatsapp_bot", pending.ConversationID); err != nil {
			slog.Error("cancel: cancel block", "error", err)
		}

		m.birdClient.SendText(phone, pending.ConversationID, "Tu cita ha sido cancelada.\n\n"+
			"Si deseas reagendar, puedes escribirnos cuando lo necesites.")

		// Leave feed item open + assign to agent for follow-up
		if pending.ConversationID != "" {
			m.birdClient.UpdateFeedItem(pending.ConversationID, pending.BirdMessageID,
				false, m.cfg.BirdTeamFallback, "")
		}

		// Cambio 13: Check waiting list for freed CUPS
		for _, proc := range appt.Procedures {
			if proc.CupCode != "" {
				go m.CheckWaitingListForCups(ctx, proc.CupCode)
			}
		}

		// Log event
		if m.tracker != nil {
			elapsed := time.Since(pending.CreatedAt).Minutes()
			m.tracker.LogEvent(ctx, "", phone, "notification_cancelled",
				map[string]interface{}{
					"appointment_id":    pending.AppointmentID,
					"response_time_min": int(elapsed),
					"conversation_id":   pending.ConversationID,
				})
		}

		slog.Info("proactive cancellation success",
			"phone", phone,
			"appointment_id", pending.AppointmentID,
			"block_size", len(block))
	}
}

// handleConfirmationTimeout handles the 6-hour no-response case.
// NOTE: Caller (handleTimeout) already removed pending from sync.Map and DB via LoadAndDelete.
// If retrying, we re-store the pending notification.
func (m *NotificationManager) handleConfirmationTimeout(pending *PendingNotification) {
	if pending.RetryCount < 2 {
		// Send follow-up message
		m.birdClient.SendText(pending.Phone, pending.ConversationID,
			"Sigues ahi?\n\nTe enviamos un recordatorio de tu cita. "+
				"Por favor confirma o cancela para que podamos gestionar tu espacio.")

		// Escalate to agent in Bird Inbox
		if pending.ConversationID != "" {
			m.birdClient.UpdateFeedItem(pending.ConversationID, pending.BirdMessageID,
				false, m.cfg.BirdTeamFallback, "")
		}

		// Re-register with retry++ and new timer (re-store since LoadAndDelete removed it)
		pending.RetryCount++
		pending.Timer = time.AfterFunc(6*time.Hour, func() {
			m.handleTimeout(pending.Phone)
		})
		m.pending.Store(pending.Phone, pending)

		// Persist retry to DB
		if m.persister != nil {
			expiresAt := time.Now().Add(6 * time.Hour)
			if err := m.persister.Upsert(context.Background(), pending.Phone, pending.Type,
				pending.AppointmentID, pending.WaitingListID, pending.BirdMessageID, pending.ConversationID,
				pending.RetryCount, expiresAt); err != nil {
				slog.Error("persist retry notification", "phone", pending.Phone, "error", err)
			}
		}

		slog.Info("proactive followup sent", "phone", pending.Phone, "retry", pending.RetryCount)
	} else {
		// Max retries reached — will be picked up by IVR at 15:00
		slog.Info("proactive no response, max retries",
			"phone", pending.Phone,
			"appointment_id", pending.AppointmentID,
			"retries", pending.RetryCount)
	}
}
