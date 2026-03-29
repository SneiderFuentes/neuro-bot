package telegram

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestAlertHandler_ChannelFull_DoesNotBlock(t *testing.T) {
	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewAlertHandler(inner, &Client{})

	// Fill the channel completely
	for i := 0; i < channelSize; i++ {
		handler.ch <- "filler"
	}

	// This should NOT block — the default case drops the alert
	done := make(chan struct{})
	go func() {
		record := slog.NewRecord(time.Now(), slog.LevelError, "overflow test", 0)
		_ = handler.Handle(context.Background(), record)
		close(done)
	}()

	select {
	case <-done:
		// OK — Handle returned without blocking
	case <-time.After(2 * time.Second):
		t.Fatal("Handle() blocked when channel is full")
	}

	if len(handler.ch) != channelSize {
		t.Errorf("expected channel still full (%d), got %d", channelSize, len(handler.ch))
	}
}

func TestAlertHandler_DedupPreventsRepeat(t *testing.T) {
	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewAlertHandler(inner, &Client{})

	record := slog.NewRecord(time.Now(), slog.LevelError, "duplicate test", 0)

	// First call should enqueue
	_ = handler.Handle(context.Background(), record)
	if len(handler.ch) != 1 {
		t.Errorf("expected 1 message in channel, got %d", len(handler.ch))
	}

	// Second identical call should be deduped
	_ = handler.Handle(context.Background(), record)
	if len(handler.ch) != 1 {
		t.Errorf("expected still 1 message (deduped), got %d", len(handler.ch))
	}
}

func TestAlertHandler_BelowError_NotSent(t *testing.T) {
	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewAlertHandler(inner, &Client{})

	record := slog.NewRecord(time.Now(), slog.LevelWarn, "warning only", 0)
	_ = handler.Handle(context.Background(), record)

	if len(handler.ch) != 0 {
		t.Errorf("expected 0 messages for WARN level, got %d", len(handler.ch))
	}
}
