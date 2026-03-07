package utils

import (
	"testing"
	"time"
)

func TestFormatFriendlyDateShort(t *testing.T) {
	// 2026-03-20 is a Friday
	date := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	got := FormatFriendlyDateShort(date)
	expected := "Vie 20/03"
	if got != expected {
		t.Errorf("FormatFriendlyDateShort = %q, want %q", got, expected)
	}
}

func TestFormatFriendlyDateShort_Sunday(t *testing.T) {
	// 2026-03-22 is a Sunday
	date := time.Date(2026, 3, 22, 0, 0, 0, 0, time.UTC)
	got := FormatFriendlyDateShort(date)
	expected := "Dom 22/03"
	if got != expected {
		t.Errorf("FormatFriendlyDateShort = %q, want %q", got, expected)
	}
}

func TestFormatFriendlyDateShortStr_Valid(t *testing.T) {
	got := FormatFriendlyDateShortStr("2026-03-20")
	expected := "Vie 20/03"
	if got != expected {
		t.Errorf("FormatFriendlyDateShortStr = %q, want %q", got, expected)
	}
}

func TestFormatFriendlyDateShortStr_Invalid(t *testing.T) {
	got := FormatFriendlyDateShortStr("not-a-date")
	if got != "not-a-date" {
		t.Errorf("expected raw string back for invalid date, got %q", got)
	}
}

func TestFormatFriendlyDate(t *testing.T) {
	// 2026-03-20 is a Friday
	date := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	got := FormatFriendlyDate(date)
	expected := "Viernes 20 de Marzo de 2026"
	if got != expected {
		t.Errorf("FormatFriendlyDate = %q, want %q", got, expected)
	}
}

func TestFormatFriendlyDate_January(t *testing.T) {
	// 2026-01-01 is a Thursday
	date := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := FormatFriendlyDate(date)
	expected := "Jueves 1 de Enero de 2026"
	if got != expected {
		t.Errorf("FormatFriendlyDate = %q, want %q", got, expected)
	}
}

func TestFormatFriendlyDate_December(t *testing.T) {
	// 2026-12-25 is a Friday
	date := time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)
	got := FormatFriendlyDate(date)
	expected := "Viernes 25 de Diciembre de 2026"
	if got != expected {
		t.Errorf("FormatFriendlyDate = %q, want %q", got, expected)
	}
}
