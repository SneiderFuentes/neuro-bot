package notifications

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/config"
	"github.com/neuro-bot/neuro-bot/internal/domain"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
)

// PendingNotification tracks a proactive notification awaiting response.
type PendingNotification struct {
	Type           string // "confirmation", "reschedule", "cancellation", "waiting_list"
	Phone          string
	AppointmentID  string
	WaitingListID  string // only for waiting_list type
	BirdMessageID  string
	ConversationID string
	Timer          *time.Timer
	RetryCount     int
	CreatedAt      time.Time
}

// WaitingListFinder reads/updates waiting list entries (avoids importing repo/local directly).
type WaitingListFinder interface {
	FindByID(ctx context.Context, id string) (*domain.WaitingListEntry, error)
	UpdateStatus(ctx context.Context, id, status string) error
}

// SessionCreator creates sessions and sets context (avoids importing repo/local directly).
type SessionCreator interface {
	Create(ctx context.Context, s *session.Session) error
	SetContextBatch(ctx context.Context, sessionID string, kvs map[string]string) error
}

// VirtualEnqueuer enqueues virtual messages for the worker pool.
type VirtualEnqueuer interface {
	EnqueueVirtual(phone string)
}

// PreparationFinder looks up procedure preparation data.
type PreparationFinder interface {
	FindByCode(ctx context.Context, code string) (*domain.Procedure, error)
}

// NotificationPersister persists pending notifications to the database.
type NotificationPersister interface {
	Upsert(ctx context.Context, phone, nType, apptID, wlID, birdMsgID, convID string, retryCount int, expiresAt time.Time) error
	Delete(ctx context.Context, phone string) error
	FindExpired(ctx context.Context) ([]PendingRow, error)
	FindAll(ctx context.Context) ([]PendingRow, error)
}

// PendingRow represents a pending notification row from the database.
type PendingRow struct {
	Phone          string
	Type           string
	AppointmentID  string
	WaitingListID  string
	BirdMessageID  string
	ConversationID string
	RetryCount     int
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

// EventLogger logs notification events for auditing.
type EventLogger interface {
	LogEvent(ctx context.Context, sessionID, phone, eventType string, data map[string]interface{})
}

// SlotSearcher checks slot availability (used by Cambio 13 real-time WL check).
type SlotSearcher interface {
	GetAvailableSlots(ctx context.Context, query services.SlotQuery) ([]services.AvailableSlot, error)
}

// FutureAppointmentChecker checks if a patient already has a future appointment for a CUPS.
type FutureAppointmentChecker interface {
	HasFutureForCup(ctx context.Context, patientID, cupCode string) (bool, error)
}

// WaitingListChecker reads and updates waiting list entries for real-time notification.
type WaitingListChecker interface {
	GetWaitingByCups(ctx context.Context, cupsCode string, limit int) ([]domain.WaitingListEntry, error)
	MarkNotified(ctx context.Context, id string) error
	UpdateStatus(ctx context.Context, id, status string) error
}

// NotificationManager handles responses to proactive WhatsApp templates.
type NotificationManager struct {
	pending         sync.Map // phone → *PendingNotification
	birdClient      *bird.Client
	apptSvc         *services.AppointmentService
	cfg             *config.Config
	waitingListRepo WaitingListFinder
	sessionRepo     SessionCreator
	workerPool      VirtualEnqueuer
	procRepo        PreparationFinder
	persister       NotificationPersister
	tracker         EventLogger

	// Cambio 13: real-time WL notification on cancellation
	slotSearcher    SlotSearcher
	apptChecker     FutureAppointmentChecker
	wlChecker       WaitingListChecker
}

// NewNotificationManager creates a new notification manager.
func NewNotificationManager(birdClient *bird.Client, apptSvc *services.AppointmentService, cfg *config.Config) *NotificationManager {
	return &NotificationManager{
		birdClient: birdClient,
		apptSvc:    apptSvc,
		cfg:        cfg,
	}
}

// SetWaitingListDeps injects Phase 13 dependencies after construction.
func (m *NotificationManager) SetWaitingListDeps(wlRepo WaitingListFinder, sessRepo SessionCreator, wp VirtualEnqueuer) {
	m.waitingListRepo = wlRepo
	m.sessionRepo = sessRepo
	m.workerPool = wp
}

// SetProcedureRepo injects the procedure repository for preparation lookups.
func (m *NotificationManager) SetProcedureRepo(repo PreparationFinder) {
	m.procRepo = repo
}

// SetPersister injects the database persister for pending notifications.
func (m *NotificationManager) SetPersister(p NotificationPersister) {
	m.persister = p
}

// SetTracker injects the event logger for auditing.
func (m *NotificationManager) SetTracker(t EventLogger) {
	m.tracker = t
}

// SetWaitingListCheckDeps injects Cambio 13 dependencies for real-time WL notification.
func (m *NotificationManager) SetWaitingListCheckDeps(ss SlotSearcher, ac FutureAppointmentChecker, wlc WaitingListChecker) {
	m.slotSearcher = ss
	m.apptChecker = ac
	m.wlChecker = wlc
}

// RegisterPending registers a pending notification with a 6-hour timeout.
func (m *NotificationManager) RegisterPending(notif PendingNotification) {
	notif.CreatedAt = time.Now()
	expiresAt := notif.CreatedAt.Add(6 * time.Hour)

	// In-memory timer (handles timeout while running)
	notif.Timer = time.AfterFunc(6*time.Hour, func() {
		m.handleTimeout(notif.Phone)
	})

	m.pending.Store(notif.Phone, &notif)

	// Persist to DB (survives restarts)
	if m.persister != nil {
		if err := m.persister.Upsert(context.Background(), notif.Phone, notif.Type,
			notif.AppointmentID, notif.WaitingListID, notif.BirdMessageID, notif.ConversationID,
			notif.RetryCount, expiresAt); err != nil {
			slog.Error("persist pending notification", "phone", notif.Phone, "error", err)
		}
	}

	slog.Info("pending notification registered", "phone", notif.Phone, "type", notif.Type)
}

// HandleResponse processes a patient's response to a proactive template.
// Uses LoadAndDelete to atomically claim ownership and prevent race with handleTimeout.
func (m *NotificationManager) HandleResponse(phone, payload, conversationID string) {
	val, ok := m.pending.LoadAndDelete(phone)
	if !ok {
		slog.Warn("no pending notification for phone", "phone", phone, "payload", payload)
		return
	}

	pending := val.(*PendingNotification)
	pending.Timer.Stop()
	pending.ConversationID = conversationID

	// Remove from DB
	if m.persister != nil {
		m.persister.Delete(context.Background(), phone)
	}

	normalized := normalizePostback(payload)

	switch pending.Type {
	case "confirmation":
		m.handleConfirmation(phone, normalized, pending)
	case "reschedule":
		m.handleReschedule(phone, normalized, pending)
	case "cancellation":
		m.handleCancellation(phone, normalized, pending)
	case "waiting_list":
		if m.waitingListRepo != nil {
			m.handleWaitingList(phone, normalized, pending)
		}
	}
}

// HasPending checks if there's a pending notification for a phone number.
func (m *NotificationManager) HasPending(phone string) bool {
	_, ok := m.pending.Load(phone)
	return ok
}

// PendingCount returns the number of pending notifications.
func (m *NotificationManager) PendingCount() int {
	count := 0
	m.pending.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// RestorePending loads pending notifications from the database on startup.
// Already-expired entries are processed immediately via handleTimeout.
func (m *NotificationManager) RestorePending(ctx context.Context) {
	if m.persister == nil {
		return
	}

	rows, err := m.persister.FindAll(ctx)
	if err != nil {
		slog.Error("restore pending notifications", "error", err)
		return
	}

	now := time.Now()
	restored := 0
	expired := 0

	for _, row := range rows {
		notif := &PendingNotification{
			Type:           row.Type,
			Phone:          row.Phone,
			AppointmentID:  row.AppointmentID,
			WaitingListID:  row.WaitingListID,
			BirdMessageID:  row.BirdMessageID,
			ConversationID: row.ConversationID,
			RetryCount:     row.RetryCount,
			CreatedAt:      row.CreatedAt,
		}

		if now.After(row.ExpiresAt) {
			// Already expired — process timeout immediately
			m.pending.Store(row.Phone, notif)
			go m.handleTimeout(row.Phone)
			expired++
			continue
		}

		// Still valid — set timer for remaining duration
		remaining := time.Until(row.ExpiresAt)
		phone := row.Phone
		notif.Timer = time.AfterFunc(remaining, func() {
			m.handleTimeout(phone)
		})
		m.pending.Store(row.Phone, notif)
		restored++
	}

	if restored > 0 || expired > 0 {
		slog.Info("pending notifications restored", "restored", restored, "expired", expired)
	}
}

// StartExpirationChecker runs a ticker that checks for expired notifications in the DB.
// This catches any expirations missed by in-memory timers (e.g., after restart + race).
func (m *NotificationManager) StartExpirationChecker(ctx context.Context) {
	if m.persister == nil {
		return
	}

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkExpired(ctx)
		}
	}
}

func (m *NotificationManager) checkExpired(ctx context.Context) {
	rows, err := m.persister.FindExpired(ctx)
	if err != nil {
		slog.Error("check expired notifications", "error", err)
		return
	}

	for _, row := range rows {
		// Only process if still in sync.Map (LoadAndDelete atomicity prevents double-processing)
		if _, ok := m.pending.Load(row.Phone); ok {
			m.handleTimeout(row.Phone)
		} else {
			// Stale DB row — remove it
			m.persister.Delete(ctx, row.Phone)
		}
	}
}

// handleTimeout is called when a patient doesn't respond within 6 hours.
// Uses LoadAndDelete to atomically claim ownership and prevent race with HandleResponse.
func (m *NotificationManager) handleTimeout(phone string) {
	val, ok := m.pending.LoadAndDelete(phone)
	if !ok {
		return
	}

	pending := val.(*PendingNotification)

	// Remove from DB (will be re-inserted if retry)
	if m.persister != nil {
		m.persister.Delete(context.Background(), phone)
	}

	// Log timeout event
	if m.tracker != nil {
		m.tracker.LogEvent(context.Background(), "", phone, "notification_timeout",
			map[string]interface{}{
				"type":            pending.Type,
				"appointment_id":  pending.AppointmentID,
				"retry":           pending.RetryCount,
				"conversation_id": pending.ConversationID,
			})
	}

	switch pending.Type {
	case "confirmation":
		m.handleConfirmationTimeout(pending)
	case "reschedule":
		m.handleConfirmationTimeout(pending) // Same behavior
	case "cancellation":
		// No timeout action — already removed from sync.Map and DB
	case "waiting_list":
		if m.waitingListRepo != nil {
			m.handleWaitingListTimeout(pending)
		}
	}
}

// normalizePostback maps Bird template postbacks to internal actions.
func normalizePostback(payload string) string {
	switch payload {
	case "confirm":
		return "confirm"
	case "cancelar": // Confirmation flow uses "cancelar"
		return "cancel"
	case "cancel": // Reschedule flow uses "cancel"
		return "cancel"
	case "understood":
		return "acknowledge"
	case "reprogramar": // Self-service reschedule button
		return "reschedule"
	case "reschedule": // Legacy cancellation flow button
		return "reschedule"
	case "wl_schedule":
		return "schedule"
	case "wl_decline":
		return "decline"
	default:
		return payload
	}
}
