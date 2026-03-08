package local

import (
	"context"
	"database/sql"
	"time"
)

// SchedulerRunRepo tracks the last successful execution of scheduled tasks.
type SchedulerRunRepo struct {
	db *sql.DB
}

func NewSchedulerRunRepo(db *sql.DB) *SchedulerRunRepo {
	return &SchedulerRunRepo{db: db}
}

// GetLastRun returns the last successful run time for a task.
// Returns zero time if the task has never run.
func (r *SchedulerRunRepo) GetLastRun(ctx context.Context, taskName string) (time.Time, error) {
	var lastRun time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT last_run_at FROM scheduler_runs WHERE task_name = ?`,
		taskName).Scan(&lastRun)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	return lastRun, err
}

// SetLastRun records that a task ran successfully at the given time.
func (r *SchedulerRunRepo) SetLastRun(ctx context.Context, taskName string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO scheduler_runs (task_name, last_run_at)
		 VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE last_run_at = VALUES(last_run_at)`,
		taskName, at)
	return err
}
