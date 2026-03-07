package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPhoneMutex_LockAcquires(t *testing.T) {
	pm := NewPhoneMutex()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := pm.Lock(ctx, "+573001234567")
	if err != nil {
		t.Fatal(err)
	}
	pm.Unlock("+573001234567")
}

func TestPhoneMutex_SamePhoneBlocks(t *testing.T) {
	pm := NewPhoneMutex()
	ctx := context.Background()

	// Acquire lock
	err := pm.Lock(ctx, "+573001234567")
	if err != nil {
		t.Fatal(err)
	}

	// Try to acquire same phone with short timeout
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	err = pm.Lock(shortCtx, "+573001234567")
	if err == nil {
		t.Error("expected timeout error when same phone is locked")
	}

	pm.Unlock("+573001234567")
}

func TestPhoneMutex_ContextCancel(t *testing.T) {
	pm := NewPhoneMutex()
	ctx := context.Background()

	// Acquire first
	pm.Lock(ctx, "+573001234567")

	// Cancel context
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel() // cancel immediately

	err := pm.Lock(cancelCtx, "+573001234567")
	if err == nil {
		t.Error("expected error on cancelled context")
	}

	pm.Unlock("+573001234567")
}

func TestPhoneMutex_DifferentPhonesNoBlock(t *testing.T) {
	pm := NewPhoneMutex()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err1 := pm.Lock(ctx, "+573001111111")
	if err1 != nil {
		t.Fatal(err1)
	}

	err2 := pm.Lock(ctx, "+573002222222")
	if err2 != nil {
		t.Fatal("different phones should not block each other")
	}

	pm.Unlock("+573001111111")
	pm.Unlock("+573002222222")
}

func TestPhoneMutex_ConcurrentSerialization(t *testing.T) {
	pm := NewPhoneMutex()
	phone := "+573001234567"
	counter := 0
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := pm.Lock(ctx, phone); err != nil {
				return
			}
			defer pm.Unlock(phone)

			mu.Lock()
			counter++
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)
		}()
	}

	wg.Wait()

	mu.Lock()
	if counter != 5 {
		t.Errorf("expected all 5 goroutines to complete, got %d", counter)
	}
	mu.Unlock()
}

func TestPhoneMutex_CleanupRemovesOldLocks(t *testing.T) {
	pm := NewPhoneMutex()
	ctx := context.Background()

	// Acquire and release a lock
	phone := "+573001234567"
	if err := pm.Lock(ctx, phone); err != nil {
		t.Fatal(err)
	}
	pm.Unlock(phone)

	// Manually set lastUsed to 15 minutes ago so cleanup will remove it
	val, ok := pm.locks.Load(phone)
	if !ok {
		t.Fatal("expected lock to exist after unlock")
	}
	pl := val.(*phoneLock)
	pl.setLastUsed(time.Now().Add(-15 * time.Minute))

	// Run cleanup manually by iterating like StartCleanup does
	threshold := time.Now().Add(-10 * time.Minute)
	pm.locks.Range(func(key, value interface{}) bool {
		lock := value.(*phoneLock)
		if lock.refCount.Load() == 0 && lock.getLastUsed().Before(threshold) {
			pm.locks.Delete(key)
		}
		return true
	})

	// Lock should have been cleaned up
	if _, exists := pm.locks.Load(phone); exists {
		t.Error("expected old lock to be removed by cleanup")
	}
}

func TestPhoneMutex_CleanupKeepsActiveLocks(t *testing.T) {
	pm := NewPhoneMutex()
	ctx := context.Background()

	phone := "+573001234567"
	if err := pm.Lock(ctx, phone); err != nil {
		t.Fatal(err)
	}
	// Don't unlock — lock is active (refCount > 0)

	// Set lastUsed to old time
	val, _ := pm.locks.Load(phone)
	pl := val.(*phoneLock)
	pl.setLastUsed(time.Now().Add(-15 * time.Minute))

	// Cleanup should NOT remove it because refCount > 0
	threshold := time.Now().Add(-10 * time.Minute)
	pm.locks.Range(func(key, value interface{}) bool {
		lock := value.(*phoneLock)
		if lock.refCount.Load() == 0 && lock.getLastUsed().Before(threshold) {
			pm.locks.Delete(key)
		}
		return true
	})

	if _, exists := pm.locks.Load(phone); !exists {
		t.Error("active lock should NOT be removed by cleanup")
	}

	pm.Unlock(phone)
}

func TestPhoneMutex_StartCleanup_ContextCancellation(t *testing.T) {
	pm := NewPhoneMutex()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		pm.StartCleanup(ctx)
		close(done)
	}()

	// Cancel immediately
	cancel()

	// Should return quickly
	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("StartCleanup did not exit after context cancellation")
	}
}
