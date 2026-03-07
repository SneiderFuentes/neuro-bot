package scheduler

import (
	"context"
	"log/slog"
	"time"
)

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
}

// NewScheduler creates a new scheduler with the given timezone.
func NewScheduler(tz *time.Location) *Scheduler {
	return &Scheduler{timezone: tz}
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
			}
		}(task)
	}

	return executed
}
