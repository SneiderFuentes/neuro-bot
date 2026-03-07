package statemachine

import (
	"context"
	"testing"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/session"
)

func configSess(state string) *session.Session {
	return &session.Session{
		ID:           "sess-cfg-1",
		PhoneNumber:  "+573001234567",
		CurrentState: state,
		Status:       session.StatusActive,
		Context:      make(map[string]string),
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(2 * time.Hour),
	}
}

func cfgTextMsg(text string) bird.InboundMessage {
	return bird.InboundMessage{
		ID: "msg-cfg-1", Phone: "+573001234567",
		MessageType: "text", Text: text,
		ReceivedAt: time.Now(),
	}
}

func cfgPostbackMsg(payload string) bird.InboundMessage {
	return bird.InboundMessage{
		ID: "msg-cfg-1", Phone: "+573001234567",
		MessageType: "text", IsPostback: true, PostbackPayload: payload,
		ReceivedAt: time.Now(),
	}
}

func cfgImageMsg() bird.InboundMessage {
	return bird.InboundMessage{
		ID: "msg-cfg-1", Phone: "+573001234567",
		MessageType: "image",
		ReceivedAt:  time.Now(),
	}
}

// ==================== InputButton ====================

func TestRegisterWithConfig_ButtonValid(t *testing.T) {
	m := NewMachine()
	var called bool
	var gotPayload string

	m.RegisterWithConfig("TEST_BTN", HandlerConfig{
		InputType: InputButton,
		Options:   []string{"opt_a", "opt_b"},
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			called = true
			gotPayload = ValidatedPayload(ctx)
			return NewResult("NEXT_STATE"), nil
		},
	})

	sess := configSess("TEST_BTN")
	result, err := m.handlers["TEST_BTN"](context.Background(), sess, cfgPostbackMsg("opt_a"))
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("handler was not called")
	}
	if gotPayload != "opt_a" {
		t.Errorf("expected payload 'opt_a', got %q", gotPayload)
	}
	if result.NextState != "NEXT_STATE" {
		t.Errorf("expected NEXT_STATE, got %s", result.NextState)
	}
}

func TestRegisterWithConfig_ButtonNumeric(t *testing.T) {
	m := NewMachine()
	var gotPayload string

	m.RegisterWithConfig("TEST_BTN", HandlerConfig{
		InputType: InputButton,
		Options:   []string{"opt_a", "opt_b"},
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			gotPayload = ValidatedPayload(ctx)
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_BTN")
	result, err := m.handlers["TEST_BTN"](context.Background(), sess, cfgTextMsg("2"))
	if err != nil {
		t.Fatal(err)
	}
	if gotPayload != "opt_b" {
		t.Errorf("expected payload 'opt_b', got %q", gotPayload)
	}
	if result.NextState != "NEXT" {
		t.Errorf("expected NEXT, got %s", result.NextState)
	}
}

func TestRegisterWithConfig_ButtonInvalid(t *testing.T) {
	m := NewMachine()
	handlerCalled := false

	m.RegisterWithConfig("TEST_BTN", HandlerConfig{
		InputType: InputButton,
		Options:   []string{"opt_a", "opt_b"},
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			handlerCalled = true
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_BTN")
	result, err := m.handlers["TEST_BTN"](context.Background(), sess, cfgTextMsg("garbage"))
	if err != nil {
		t.Fatal(err)
	}
	if handlerCalled {
		t.Error("handler should not have been called for invalid input")
	}
	if result.NextState != "TEST_BTN" {
		t.Errorf("expected retry to same state, got %s", result.NextState)
	}
}

func TestRegisterWithConfig_ButtonEscalation(t *testing.T) {
	m := NewMachine()

	m.RegisterWithConfig("TEST_BTN", HandlerConfig{
		InputType: InputButton,
		Options:   []string{"opt_a"},
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_BTN")
	sess.RetryCount = 2 // next retry will be 3 = escalation

	result, err := m.handlers["TEST_BTN"](context.Background(), sess, cfgTextMsg("garbage"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT, got %s", result.NextState)
	}
	if sess.RetryCount != 0 {
		t.Error("expected RetryCount reset after escalation")
	}
}

func TestRegisterWithConfig_ButtonRetryPrompt(t *testing.T) {
	m := NewMachine()
	promptCalled := false

	m.RegisterWithConfig("TEST_BTN", HandlerConfig{
		InputType: InputButton,
		Options:   []string{"opt_a"},
		RetryPrompt: func(sess *session.Session, result *StateResult) {
			promptCalled = true
			result.Messages = append(result.Messages, &ButtonMessage{
				Text:    "Pick one:",
				Buttons: []Button{{Text: "A", Payload: "opt_a"}},
			})
		},
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_BTN")
	result, _ := m.handlers["TEST_BTN"](context.Background(), sess, cfgTextMsg("bad"))
	if !promptCalled {
		t.Error("RetryPrompt was not called")
	}
	// Default error text + retry prompt = 2 messages
	if len(result.Messages) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(result.Messages))
	}
}

func TestRegisterWithConfig_ButtonRetryPromptSkippedOnEscalation(t *testing.T) {
	m := NewMachine()
	promptCalled := false

	m.RegisterWithConfig("TEST_BTN", HandlerConfig{
		InputType: InputButton,
		Options:   []string{"opt_a"},
		RetryPrompt: func(sess *session.Session, result *StateResult) {
			promptCalled = true
		},
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_BTN")
	sess.RetryCount = maxRetries - 1 // next retry triggers escalation

	result, _ := m.handlers["TEST_BTN"](context.Background(), sess, cfgTextMsg("bad"))
	if promptCalled {
		t.Error("RetryPrompt should NOT be called when escalating to agent")
	}
	if result.NextState != StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT, got %s", result.NextState)
	}
}

// ==================== InputText ====================

func TestRegisterWithConfig_TextValid(t *testing.T) {
	m := NewMachine()
	handlerCalled := false

	m.RegisterWithConfig("TEST_TXT", HandlerConfig{
		InputType:    InputText,
		TextValidate: func(s string) bool { return len(s) >= 3 },
		ErrorMsg:     "At least 3 chars.",
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			handlerCalled = true
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_TXT")
	result, err := m.handlers["TEST_TXT"](context.Background(), sess, cfgTextMsg("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if !handlerCalled {
		t.Error("handler was not called")
	}
	if result.NextState != "NEXT" {
		t.Errorf("expected NEXT, got %s", result.NextState)
	}
}

func TestRegisterWithConfig_TextInvalid(t *testing.T) {
	m := NewMachine()

	m.RegisterWithConfig("TEST_TXT", HandlerConfig{
		InputType:    InputText,
		TextValidate: func(s string) bool { return len(s) >= 5 },
		ErrorMsg:     "Too short.",
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_TXT")
	result, err := m.handlers["TEST_TXT"](context.Background(), sess, cfgTextMsg("ab"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != "TEST_TXT" {
		t.Errorf("expected retry to same state, got %s", result.NextState)
	}
}

func TestRegisterWithConfig_TextEscalation(t *testing.T) {
	m := NewMachine()

	m.RegisterWithConfig("TEST_TXT", HandlerConfig{
		InputType:    InputText,
		TextValidate: func(s string) bool { return false },
		ErrorMsg:     "Bad input.",
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_TXT")
	sess.RetryCount = 2

	result, _ := m.handlers["TEST_TXT"](context.Background(), sess, cfgTextMsg("x"))
	if result.NextState != StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT, got %s", result.NextState)
	}
}

// ==================== InputImage ====================

func TestRegisterWithConfig_ImageValid(t *testing.T) {
	m := NewMachine()
	handlerCalled := false

	m.RegisterWithConfig("TEST_IMG", HandlerConfig{
		InputType: InputImage,
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			handlerCalled = true
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_IMG")
	result, err := m.handlers["TEST_IMG"](context.Background(), sess, cfgImageMsg())
	if err != nil {
		t.Fatal(err)
	}
	if !handlerCalled {
		t.Error("handler was not called")
	}
	if result.NextState != "NEXT" {
		t.Errorf("expected NEXT, got %s", result.NextState)
	}
}

func TestRegisterWithConfig_ImageNotImage(t *testing.T) {
	m := NewMachine()

	m.RegisterWithConfig("TEST_IMG", HandlerConfig{
		InputType: InputImage,
		ErrorMsg:  "Send an image please.",
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_IMG")
	result, err := m.handlers["TEST_IMG"](context.Background(), sess, cfgTextMsg("not an image"))
	if err != nil {
		t.Fatal(err)
	}
	if result.NextState != "TEST_IMG" {
		t.Errorf("expected retry to same state, got %s", result.NextState)
	}
}

func TestRegisterWithConfig_ImageEscalation(t *testing.T) {
	m := NewMachine()

	m.RegisterWithConfig("TEST_IMG", HandlerConfig{
		InputType: InputImage,
		ErrorMsg:  "Image required.",
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_IMG")
	sess.RetryCount = 2

	result, _ := m.handlers["TEST_IMG"](context.Background(), sess, cfgTextMsg("text"))
	if result.NextState != StateEscalateToAgent {
		t.Errorf("expected ESCALATE_TO_AGENT, got %s", result.NextState)
	}
}

// ==================== InputAny ====================

func TestRegisterWithConfig_AnyPassthrough(t *testing.T) {
	m := NewMachine()
	handlerCalled := false

	m.RegisterWithConfig("TEST_ANY", HandlerConfig{
		InputType: InputAny,
		Handler: func(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*StateResult, error) {
			handlerCalled = true
			return NewResult("NEXT"), nil
		},
	})

	sess := configSess("TEST_ANY")
	result, err := m.handlers["TEST_ANY"](context.Background(), sess, cfgTextMsg("anything"))
	if err != nil {
		t.Fatal(err)
	}
	if !handlerCalled {
		t.Error("handler was not called")
	}
	if result.NextState != "NEXT" {
		t.Errorf("expected NEXT, got %s", result.NextState)
	}
}

// ==================== ValidatedPayload ====================

func TestValidatedPayload_Empty(t *testing.T) {
	ctx := context.Background()
	if got := ValidatedPayload(ctx); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
