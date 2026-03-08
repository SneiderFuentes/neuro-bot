package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// RunRepo persists the last successful execution of scheduled tasks.
type RunRepo interface {
	GetLastRun(ctx context.Context, taskName string) (time.Time, error)
	SetLastRun(ctx context.Context, taskName string, at time.Time) error
}

// ScheduledTask represents a task that runs at a specific time.
type ScheduledTask struct {
	Name     string
	Hour     int
	Minute   int
	Weekdays []time.Weekday // nil = every day
	Fn       func(ctx context.Context) error
}

// Scheduler executes tasks at configured times using a 1-minute ticker.
type Scheduler struct {
	tasks    []ScheduledTask
	timezone *time.Location
	runRepo  RunRepo // optional — persists run times for crash catch-up
}

// NewScheduler creates a new scheduler with the given timezone.
func NewScheduler(tz *time.Location) *Scheduler {
	return &Scheduler{timezone: tz}
}

// SetRunRepo injects the repo for persisting task run times.
func (s *Scheduler) SetRunRepo(repo RunRepo) {
	s.runRepo = repo
}

// AddTask registers a task to be executed at the configured time.
func (s *Scheduler) AddTask(task ScheduledTask) {
	s.tasks = append(s.tasks, task)
}

// Start begins the scheduler loop. Blocks until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	lastRun := make(map[string]time.Time)

	for {
		select {
		case <-ctx.Done():
			slog.Info("scheduler stopped")
			return
		case <-ticker.C:
			now := time.Now().In(s.timezone)
			s.evaluateTasks(ctx, now, lastRun)
		}
	}
}

// RunMissedTasks checks if any tasks should have run today but haven't.
// Only catches up tasks from the current day to avoid running stale historical tasks.
// Call once at startup, before Start().
func (s *Scheduler) RunMissedTasks(ctx context.Context) {
	if s.runRepo == nil {
		return
	}

	now := time.Now().In(s.timezone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.timezone)

	for _, task := range s.tasks {
		// Check weekday filter
		if task.Weekdays != nil {
			found := false
			for _, wd := range task.Weekdays {
				if wd == now.Weekday() {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Only catch up if the scheduled time has already passed today
		scheduledToday := time.Date(now.Year(), now.Month(), now.Day(), task.Hour, task.Minute, 0, 0, s.timezone)
		if now.Before(scheduledToday) {
			continue
		}

		// Check DB for last run
		lastRun, err := s.runRepo.GetLastRun(ctx, task.Name)
		if err != nil {
			slog.Error("check missed task", "task", task.Name, "error", err)
			continue
		}

		// If last run was before today, the task was missed
		if !lastRun.Before(today) {
			continue // already ran today
		}

		slog.Warn("scheduler catch-up: missed task detected",
			"task", task.Name,
			"last_run", lastRun,
			"scheduled_at", scheduledToday,
		)

		go func(t ScheduledTask) {
			slog.Info("scheduler catch-up: running missed task", "task", t.Name)
			start := time.Now()

			if err := t.Fn(ctx); err != nil {
				slog.Error("scheduler catch-up failed", "task", t.Name, "error", err,
					"duration_ms", time.Since(start).Milliseconds())
			} else {
				slog.Info("scheduler catch-up completed", "task", t.Name,
					"duration_ms", time.Since(start).Milliseconds())
				if s.runRepo != nil {
					if err := s.runRepo.SetLastRun(context.Background(), t.Name, time.Now()); err != nil {
						slog.Error("persist catch-up run", "task", t.Name, "error", err)
					}
				}
			}
		}(task)
	}
}

// evaluateTasks checks all registered tasks against the given time and runs matching ones.
// lastRun is updated in place to prevent double execution within the same minute.
func (s *Scheduler) evaluateTasks(ctx context.Context, now time.Time, lastRun map[string]time.Time) []string {
	var executed []string

	for _, task := range s.tasks {
		if now.Hour() != task.Hour || now.Minute() != task.Minute {
			continue
		}

		// Check weekday filter
		if task.Weekdays != nil {
			found := false
			for _, wd := range task.Weekdays {
				if wd == now.Weekday() {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Prevent double execution within same minute
		key := task.Name
		if last, ok := lastRun[key]; ok && now.Sub(last) < 2*time.Minute {
			continue
		}
		lastRun[key] = now
		executed = append(executed, task.Name)

		go func(t ScheduledTask) {
			slog.Info("scheduler task starting", "task", t.Name)
			start := time.Now()

			if err := t.Fn(ctx); err != nil {
				slog.Error("scheduler task failed", "task", t.Name, "error", err,
					"duration_ms", time.Since(start).Milliseconds())
			} else {
				slog.Info("scheduler task completed", "task", t.Name,
					"duration_ms", time.Since(start).Milliseconds())
				// Persist successful run time
				if s.runRepo != nil {
					if err := s.runRepo.SetLastRun(context.Background(), t.Name, now); err != nil {
						slog.Error("persist scheduler run", "task", t.Name, "error", err)
					}
				}
			}
		}(task)
	}

	return executed
}
