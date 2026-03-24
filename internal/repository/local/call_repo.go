package local

import (
	"context"
	"database/sql"
)

// CallRepo persists IVR call records to communication_calls for KPI tracking.
type CallRepo struct {
	db *sql.DB
}

func NewCallRepo(db *sql.DB) *CallRepo {
	return &CallRepo{db: db}
}

// InsertCall inserts a new IVR call record with status "initiated".
func (r *CallRepo) InsertCall(ctx context.Context, callID, phone, appointmentID string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO communication_calls (phone_number, appointment_id, call_type, status, bird_call_id)
		 VALUES (?, ?, 'ivr_reminder', 'initiated', ?)`,
		phone, appointmentID, callID)
	return err
}

// UpdateCallResult updates the status and result of an existing call by bird_call_id.
func (r *CallRepo) UpdateCallResult(ctx context.Context, callID, status, result string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE communication_calls SET status = ?, result = ? WHERE bird_call_id = ?`,
		status, result, callID)
	return err
}
