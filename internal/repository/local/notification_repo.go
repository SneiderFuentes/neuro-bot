package local

import (
	"context"
	"database/sql"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/notifications"
)

// NotificationRepo handles persistence of pending notifications.
type NotificationRepo struct {
	db *sql.DB
}

func NewNotificationRepo(db *sql.DB) *NotificationRepo {
	return &NotificationRepo{db: db}
}

// Upsert inserts or updates a pending notification (keyed by phone).
func (r *NotificationRepo) Upsert(ctx context.Context, phone, nType, apptID, wlID, birdMsgID, convID string, retryCount int, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notification_pending (phone, type, appointment_id, waiting_list_id, bird_message_id, conversation_id, retry_count, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   type = VALUES(type),
		   appointment_id = VALUES(appointment_id),
		   waiting_list_id = VALUES(waiting_list_id),
		   bird_message_id = VALUES(bird_message_id),
		   conversation_id = VALUES(conversation_id),
		   retry_count = VALUES(retry_count),
		   expires_at = VALUES(expires_at)`,
		phone, nType, apptID, wlID, birdMsgID, convID, retryCount, expiresAt)
	return err
}

// Delete removes a pending notification by phone.
func (r *NotificationRepo) Delete(ctx context.Context, phone string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM notification_pending WHERE phone = ?`, phone)
	return err
}

// FindExpired returns all pending notifications whose expires_at has passed.
func (r *NotificationRepo) FindExpired(ctx context.Context) ([]notifications.PendingRow, error) {
	return r.queryRows(ctx,
		`SELECT phone, type, COALESCE(appointment_id, ''), COALESCE(waiting_list_id, ''),
		        COALESCE(bird_message_id, ''), COALESCE(conversation_id, ''),
		        retry_count, expires_at, created_at
		 FROM notification_pending
		 WHERE expires_at < NOW()
		 ORDER BY expires_at ASC`)
}

// FindAll returns all pending notifications (used for restore on startup).
func (r *NotificationRepo) FindAll(ctx context.Context) ([]notifications.PendingRow, error) {
	return r.queryRows(ctx,
		`SELECT phone, type, COALESCE(appointment_id, ''), COALESCE(waiting_list_id, ''),
		        COALESCE(bird_message_id, ''), COALESCE(conversation_id, ''),
		        retry_count, expires_at, created_at
		 FROM notification_pending
		 ORDER BY created_at ASC`)
}

func (r *NotificationRepo) queryRows(ctx context.Context, query string) ([]notifications.PendingRow, error) {
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []notifications.PendingRow
	for rows.Next() {
		var row notifications.PendingRow
		if err := rows.Scan(
			&row.Phone, &row.Type, &row.AppointmentID, &row.WaitingListID,
			&row.BirdMessageID, &row.ConversationID,
			&row.RetryCount, &row.ExpiresAt, &row.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
