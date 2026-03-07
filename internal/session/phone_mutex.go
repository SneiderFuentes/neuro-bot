package session

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type phoneLock struct {
	mu       sync.Mutex
	refCount atomic.Int32
	lastUsed atomic.Int64 // Unix nano timestamp for atomic access
}

func (pl *phoneLock) setLastUsed(t time.Time) {
	pl.lastUsed.Store(t.UnixNano())
}

func (pl *phoneLock) getLastUsed() time.Time {
	return time.Unix(0, pl.lastUsed.Load())
}

type PhoneMutex struct {
	locks sync.Map // phone -> *phoneLock
}

func NewPhoneMutex() *PhoneMutex {
	return &PhoneMutex{}
}

// Lock adquiere el lock para un teléfono con timeout
func (pm *PhoneMutex) Lock(ctx context.Context, phone string) error {
	actual, _ := pm.locks.LoadOrStore(phone, &phoneLock{})
	pl := actual.(*phoneLock)
	pl.refCount.Add(1)

	// Intentar adquirir con timeout
	done := make(chan struct{})
	go func() {
		pl.mu.Lock()
		close(done)
	}()

	select {
	case <-done:
		pl.setLastUsed(time.Now())
		return nil
	case <-ctx.Done():
		pl.refCount.Add(-1)
		// The goroutine above will eventually acquire the lock.
		// We must release it to avoid a permanent deadlock for this phone.
		go func() {
			<-done       // wait for the goroutine to acquire
			pl.mu.Unlock() // immediately release since we timed out
		}()
		return fmt.Errorf("phone lock timeout for %s: %w", phone, ctx.Err())
	}
}

// Unlock libera el lock para un teléfono
func (pm *PhoneMutex) Unlock(phone string) {
	if val, ok := pm.locks.Load(phone); ok {
		pl := val.(*phoneLock)
		pl.setLastUsed(time.Now())
		pl.refCount.Add(-1)
		pl.mu.Unlock()
	}
}

// StartCleanup inicia la goroutine de limpieza de locks inactivos (cada 5 min, umbral 10 min)
func (pm *PhoneMutex) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			threshold := time.Now().Add(-10 * time.Minute)
			cleaned := 0
			pm.locks.Range(func(key, value interface{}) bool {
				pl := value.(*phoneLock)
				if pl.refCount.Load() == 0 && pl.getLastUsed().Before(threshold) {
					pm.locks.Delete(key)
					cleaned++
				}
				return true
			})
			if cleaned > 0 {
				slog.Debug("phone locks cleaned", "count", cleaned)
			}
		}
	}
}
