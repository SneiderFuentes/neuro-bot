package session

import "testing"

func TestSession_GetContext_NilMap(t *testing.T) {
	s := &Session{Context: nil}
	val := s.GetContext("any_key")
	if val != "" {
		t.Errorf("expected empty string for nil context, got %q", val)
	}
}

func TestSession_GetContext_ExistingKey(t *testing.T) {
	s := &Session{Context: map[string]string{"foo": "bar"}}
	if val := s.GetContext("foo"); val != "bar" {
		t.Errorf("expected 'bar', got %q", val)
	}
}

func TestSession_GetContext_MissingKey(t *testing.T) {
	s := &Session{Context: map[string]string{"foo": "bar"}}
	if val := s.GetContext("missing"); val != "" {
		t.Errorf("expected empty string for missing key, got %q", val)
	}
}

func TestSession_SetContext_NilMap(t *testing.T) {
	s := &Session{Context: nil}
	s.SetContext("key1", "value1")
	if s.Context == nil {
		t.Fatal("expected context to be initialized")
	}
	if s.Context["key1"] != "value1" {
		t.Errorf("expected 'value1', got %q", s.Context["key1"])
	}
}

func TestSession_SetContext_ExistingMap(t *testing.T) {
	s := &Session{Context: map[string]string{"existing": "val"}}
	s.SetContext("new", "value")
	if s.Context["existing"] != "val" {
		t.Error("expected existing key preserved")
	}
	if s.Context["new"] != "value" {
		t.Errorf("expected 'value', got %q", s.Context["new"])
	}
}
