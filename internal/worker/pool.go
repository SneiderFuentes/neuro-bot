package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/services"
	"github.com/neuro-bot/neuro-bot/internal/session"
	"github.com/neuro-bot/neuro-bot/internal/statemachine"
	"github.com/neuro-bot/neuro-bot/internal/tracking"
)

const (
	defaultWorkers        = 10
	defaultQueueSize      = 100
	maxOverflowGoroutines = 20
	dedupTTL              = 5 * time.Minute
	dedupCleanup          = 2 * time.Minute
	phoneLockTimeout      = 30 * time.Second
	agentCmdQueueSize     = 50
)

// SessionManagement abstracts session manager operations for testability.
type SessionManagement interface {
	PhoneMutex() *session.PhoneMutex
	FindOrCreate(ctx context.Context, phone string) (*session.Session, bool, error)
	RenewTimeout(ctx context.Context, sess *session.Session) error
	SaveState(ctx context.Context, sess *session.Session, state string, updateCtx map[string]string, clearCtx []string) error
	ClearAllContext(ctx context.Context, sess *session.Session) error
	Escalate(ctx context.Context, sess *session.Session, teamID string) error
	ResumeFromEscalation(ctx context.Context, sess *session.Session, targetState string) error
	Complete(ctx context.Context, sess *session.Session) error
	UpdateConversationID(ctx context.Context, phone, conversationID string) error
	SetContext(ctx context.Context, sess *session.Session, key, value string) error
}

// MessageSender abstracts outbound message sending and conversation cache for testability.
type MessageSender interface {
	SendText(phone, conversationID, text string) (string, error)
	SendButtons(phone, conversationID, text string, buttons []bird.Button) (string, error)
	SendList(phone, conversationID, body, title string, sections []bird.ListSection) (string, error)
	SendInternalText(conversationID, text string) (string, error)
	UnassignFeedItem(conversationID string, closed bool) error
	GetCachedConversationID(phone string) string
	LookupConversationByPhone(phone string) (string, error)
}

// MessageProcessor abstracts state machine processing for testability.
type MessageProcessor interface {
	Process(ctx context.Context, sess *session.Session, msg bird.InboundMessage) (*statemachine.StateResult, error)
}

// OCRAnalyzer abstracts OCR text analysis for testability.
type OCRAnalyzer interface {
	AnalyzeText(ctx context.Context, description string) (*services.OCRResult, error)
}

type MessageWorkerPool struct {
	queue          chan bird.InboundMessage
	agentCmds      chan AgentCommand
	recentMessages sync.Map // messageID -> time.Time (dedup)
	workers        int
	activeOverflow atomic.Int32
	wg             sync.WaitGroup // tracks all goroutines for graceful shutdown
	ctx            context.Context // stored from Start() for overflow goroutines

	// Dependencias (inyectadas después de creación)
	sessionManager SessionManagement
	birdClient     MessageSender
	machine        MessageProcessor
	tracker        *tracking.EventTracker
	ocrService     OCRAnalyzer
}

func NewMessageWorkerPool(workers, queueSize int) *MessageWorkerPool {
	if workers <= 0 {
		workers = defaultWorkers
	}
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	return &MessageWorkerPool{
		queue:     make(chan bird.InboundMessage, queueSize),
		agentCmds: make(chan AgentCommand, agentCmdQueueSize),
		workers:   workers,
		ctx:       context.Background(),
	}
}

// SetDependencies inyecta las dependencias necesarias para procesar mensajes
func (p *MessageWorkerPool) SetDependencies(sm SessionManagement, bc MessageSender, m MessageProcessor) {
	p.sessionManager = sm
	p.birdClient = bc
	p.machine = m
}

// SetTracker sets the event tracker for persisting events to the database.
func (p *MessageWorkerPool) SetTracker(t *tracking.EventTracker) {
	p.tracker = t
}

// SetOCRService sets the OCR service for /bot orden command processing.
func (p *MessageWorkerPool) SetOCRService(svc OCRAnalyzer) {
	p.ocrService = svc
}

// QueueStats returns the current queue size and capacity.
func (p *MessageWorkerPool) QueueStats() (size, capacity int) {
	return len(p.queue), cap(p.queue)
}

// Start inicia los worker goroutines
func (p *MessageWorkerPool) Start(ctx context.Context) {
	p.ctx = ctx
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go func(id int) {
			defer p.wg.Done()
			p.worker(ctx, id)
		}(i)
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.startDedupCleanup(ctx)
	}()
	slog.Info("worker pool started", "workers", p.workers, "queue_size", cap(p.queue))
}

// Stop waits for all workers and overflow goroutines to finish.
// Call after cancelling the context passed to Start.
func (p *MessageWorkerPool) Stop() {
	p.wg.Wait()
	dropped := len(p.queue)
	if dropped > 0 {
		slog.Warn("worker pool stopped, messages dropped from queue", "dropped", dropped)
	}
}

// Enqueue agrega un mensaje al queue. Retorna false si es duplicado o si se excede backpressure.
func (p *MessageWorkerPool) Enqueue(msg bird.InboundMessage) bool {
	// 1. Dedup check
	if _, exists := p.recentMessages.LoadOrStore(msg.ID, time.Now()); exists {
		slog.Debug("duplicate message ignored", "id", msg.ID, "phone", msg.Phone)
		return false
	}

	// 2. Intentar encolar en el channel (no bloqueante)
	select {
	case p.queue <- msg:
		return true
	default:
		// 3. Channel lleno — overflow con límite
		if p.activeOverflow.Load() >= int32(maxOverflowGoroutines) {
			slog.Error("backpressure: overflow limit reached, dropping message",
				"id", msg.ID,
				"queue_size", len(p.queue),
				"overflow_active", p.activeOverflow.Load())
			return false
		}
		p.activeOverflow.Add(1)
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer p.activeOverflow.Add(-1)
			slog.Warn("processing in overflow goroutine", "id", msg.ID)
			p.processMessage(p.ctx, msg)
		}()
		return true
	}
}

// EnqueueAgentCommand enqueues a /bot command from a human agent.
func (p *MessageWorkerPool) EnqueueAgentCommand(cmd AgentCommand) {
	select {
	case p.agentCmds <- cmd:
		slog.Info("agent command enqueued", "phone", cmd.Phone, "action", cmd.Action)
	default:
		slog.Warn("agent command queue full, dropping", "phone", cmd.Phone, "action", cmd.Action)
	}
}

func (p *MessageWorkerPool) worker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-p.queue:
			p.processMessage(ctx, msg)
		case cmd := <-p.agentCmds:
			p.processAgentCommand(ctx, cmd)
		}
	}
}

func (p *MessageWorkerPool) processMessage(parentCtx context.Context, msg bird.InboundMessage) {
	// 1. Crear contexto con timeout de 30s para adquisición del lock
	lockCtx, lockCancel := context.WithTimeout(parentCtx, phoneLockTimeout)
	defer lockCancel()

	// 2. Adquirir lock por teléfono (serializa mensajes del mismo usuario)
	if err := p.sessionManager.PhoneMutex().Lock(lockCtx, msg.Phone); err != nil {
		slog.Warn("phone lock timeout", "phone", msg.Phone, "error", err)
		return
	}
	defer p.sessionManager.PhoneMutex().Unlock(msg.Phone)

	// 3. Cargar/crear sesión
	sess, isNew, err := p.sessionManager.FindOrCreate(parentCtx, msg.Phone)
	if err != nil {
		slog.Error("session error", "phone", msg.Phone, "error", err)
		return
	}

	if isNew {
		slog.Info("new session created", "session_id", sess.ID, "phone", msg.Phone)
	}

	// 3b. Store conversationId from Bird webhook (needed for escalation)
	if msg.ConversationID != "" && sess.ConversationID != msg.ConversationID {
		slog.Debug("conversation_id_updated",
			"session_id", sess.ID,
			"phone", msg.Phone,
			"old", sess.ConversationID,
			"new", msg.ConversationID,
		)
		sess.ConversationID = msg.ConversationID
	}

	// 3c. Validate conversationID: cache check → API lookup if needed.
	// After bot restart the in-memory cache is empty, and the session may hold
	// a stale ID from a closed Bird conversation. We must verify/refresh it.
	if cached := p.birdClient.GetCachedConversationID(msg.Phone); cached != "" {
		// Cache is authoritative (populated by conversation.created webhook or prior API lookup)
		if cached != sess.ConversationID {
			slog.Debug("conversation_id_updated_from_cache",
				"session_id", sess.ID,
				"phone", msg.Phone,
				"old", sess.ConversationID,
				"new", cached,
			)
			sess.ConversationID = cached
		}
	} else {
		// Cache empty (e.g., after restart) — verify via API lookup.
		// LookupConversationByPhone populates the cache on success,
		// so subsequent messages from this phone won't trigger another lookup.
		if looked, err := p.birdClient.LookupConversationByPhone(msg.Phone); err != nil {
			slog.Warn("conversation_lookup_failed",
				"session_id", sess.ID,
				"phone", msg.Phone,
				"error", err,
			)
		} else if looked != "" {
			if looked != sess.ConversationID {
				slog.Info("conversation_id_refreshed",
					"session_id", sess.ID,
					"phone", msg.Phone,
					"old", sess.ConversationID,
					"new", looked,
				)
			}
			sess.ConversationID = looked
		} else if sess.ConversationID != "" {
			// No active conversation found — clear stale ID so messages
			// go via Channels API until a new conversation is created.
			slog.Warn("conversation_id_stale_cleared",
				"session_id", sess.ID,
				"phone", msg.Phone,
				"stale", sess.ConversationID,
			)
			sess.ConversationID = ""
		}
	}

	slog.Debug("session_state",
		"session_id", sess.ID,
		"phone", msg.Phone,
		"state", sess.CurrentState,
		"status", sess.Status,
		"retry_count", sess.RetryCount,
		"conversation_id", sess.ConversationID,
		"is_new", isNew,
	)

	// 4. Renovar timeout (siempre, incluye escaladas para mantener sesión viva con actividad)
	if err := p.sessionManager.RenewTimeout(parentCtx, sess); err != nil {
		slog.Error("renew timeout error", "phone", msg.Phone, "error", err)
	}

	// 5. If session is escalated, log but DO NOT process through state machine
	if sess.Status == session.StatusEscalated {
		slog.Info("msg during escalation (ignored by bot)",
			"session_id", sess.ID,
			"phone", msg.Phone,
			"text", msg.Text,
		)
		if p.tracker != nil {
			p.tracker.LogEvent(parentCtx, sess.ID, msg.Phone, "msg_during_escalation", map[string]interface{}{
				"text": msg.Text,
				"type": msg.MessageType,
			})
		}
		return
	}

	// 5b. Reset inactivity reminders when patient sends a message
	if r := sess.GetContext("inactivity_reminders"); r != "" && r != "0" {
		_ = p.sessionManager.SetContext(parentCtx, sess, "inactivity_reminders", "0")
	}

	// 6. Log mensaje recibido
	slog.Info("processing message",
		"session_id", sess.ID,
		"phone", msg.Phone,
		"state", sess.CurrentState,
		"type", msg.MessageType,
		"text", msg.Text,
	)

	// 7. Ejecutar state machine
	prevState := sess.CurrentState
	result, err := p.machine.Process(parentCtx, sess, msg)
	if err != nil {
		slog.Error("state machine error",
			"phone", msg.Phone,
			"state", sess.CurrentState,
			"error", err,
		)
		p.birdClient.SendText(msg.Phone, sess.ConversationID, "Lo siento, ocurrió un error. Por favor intenta de nuevo.")
		return
	}

	slog.Debug("state_transition",
		"session_id", sess.ID,
		"phone", msg.Phone,
		"from", prevState,
		"to", result.NextState,
		"messages_count", len(result.Messages),
		"events_count", len(result.Events),
		"retry_count", sess.RetryCount,
	)

	// 8. Enviar mensajes de respuesta + persistir
	p.sendAndSave(parentCtx, sess, msg.Phone, result)
}

// processAgentCommand handles /bot commands from human agents via outbound webhook.
func (p *MessageWorkerPool) processAgentCommand(parentCtx context.Context, cmd AgentCommand) {
	// 1. Acquire phone lock
	lockCtx, lockCancel := context.WithTimeout(parentCtx, phoneLockTimeout)
	defer lockCancel()

	if err := p.sessionManager.PhoneMutex().Lock(lockCtx, cmd.Phone); err != nil {
		slog.Warn("phone lock timeout (agent cmd)", "phone", cmd.Phone, "error", err)
		return
	}
	defer p.sessionManager.PhoneMutex().Unlock(cmd.Phone)

	// 2. Find escalated session
	sess, _, err := p.sessionManager.FindOrCreate(parentCtx, cmd.Phone)
	if err != nil {
		slog.Error("session error (agent cmd)", "phone", cmd.Phone, "error", err)
		return
	}
	if sess == nil || sess.Status != session.StatusEscalated {
		slog.Warn("agent command but no escalated session", "phone", cmd.Phone, "action", cmd.Action)
		return
	}

	// 3. Renovar timeout (mantiene sesión escalada viva mientras agente interactúa)
	if err := p.sessionManager.RenewTimeout(parentCtx, sess); err != nil {
		slog.Error("renew timeout error (agent cmd)", "phone", cmd.Phone, "error", err)
	}

	slog.Info("processing agent command",
		"session_id", sess.ID,
		"phone", cmd.Phone,
		"action", cmd.Action,
		"state", cmd.State,
		"data", cmd.Data,
	)

	switch cmd.Action {
	case "resume":
		p.handleAgentResume(parentCtx, sess, cmd)

	case "reset":
		p.handleAgentReset(parentCtx, sess, cmd)

	case "close":
		p.handleAgentClose(parentCtx, sess, cmd)

	case "info":
		p.handleAgentInfo(parentCtx, sess)

	case "orden":
		p.handleAgentOrder(parentCtx, sess, cmd)
	}
}

// handleAgentResume resumes a session from escalation at a specific state, optionally with corrected data.
func (p *MessageWorkerPool) handleAgentResume(ctx context.Context, sess *session.Session, cmd AgentCommand) {
	// Determine target state
	targetState := cmd.State
	if targetState == "" {
		targetState = sess.GetContext("pre_escalation_state")
	}
	if targetState == "" {
		targetState = statemachine.StateGreeting
	}

	// Resume session in DB
	if err := p.sessionManager.ResumeFromEscalation(ctx, sess, targetState); err != nil {
		slog.Error("resume from escalation failed", "error", err, "phone", cmd.Phone)
		return
	}

	// Unassign agent from Bird Inbox (bot takes over, keep item open)
	p.birdClient.UnassignFeedItem(sess.ConversationID, false)

	if cmd.Data != "" {
		// Agent provided corrected data — inject as virtual message
		p.birdClient.SendText(sess.PhoneNumber, sess.ConversationID, "Hemos retomado tu atencion. Procesando tu informacion...")

		virtualMsg := bird.InboundMessage{
			ID:          fmt.Sprintf("agent-cmd-%s-%d", cmd.Phone, time.Now().UnixNano()),
			Phone:       sess.PhoneNumber,
			Text:        cmd.Data,
			MessageType: "text",
			ReceivedAt:  time.Now(),
		}

		result, err := p.machine.Process(ctx, sess, virtualMsg)
		if err != nil {
			slog.Error("process virtual message failed", "error", err, "phone", cmd.Phone)
			return
		}
		p.sendAndSave(ctx, sess, sess.PhoneNumber, result)
	} else {
		// No data — notify patient and trigger the state handler.
		// For automatic states: executes immediately (e.g., CHECK_ENTITY).
		// For interactive states: re-displays the prompt/buttons (e.g., MAIN_MENU).
		p.birdClient.SendText(sess.PhoneNumber, sess.ConversationID, "Hemos retomado tu atencion. Continuamos con tu proceso.")

		virtualMsg := bird.InboundMessage{
			ID:          fmt.Sprintf("agent-resume-%s-%d", cmd.Phone, time.Now().UnixNano()),
			Phone:       sess.PhoneNumber,
			Text:        "__resume__",
			MessageType: "text",
			ReceivedAt:  time.Now(),
		}
		result, err := p.machine.Process(ctx, sess, virtualMsg)
		if err == nil && result != nil {
			p.sendAndSave(ctx, sess, sess.PhoneNumber, result)
		}
	}

	if p.tracker != nil {
		p.tracker.LogEvent(ctx, sess.ID, cmd.Phone, "escalation_ended", map[string]interface{}{
			"resumed_at_state": targetState,
			"data_corrected":   cmd.Data != "",
			"by":               "agent_command",
		})
	}
}

// handleAgentReset restarts the session from GREETING (like /bot without arguments).
func (p *MessageWorkerPool) handleAgentReset(ctx context.Context, sess *session.Session, cmd AgentCommand) {
	if err := p.sessionManager.ResumeFromEscalation(ctx, sess, statemachine.StateGreeting); err != nil {
		slog.Error("agent reset failed", "error", err, "phone", cmd.Phone)
		return
	}
	if err := p.sessionManager.ClearAllContext(ctx, sess); err != nil {
		slog.Error("clear context on reset failed", "error", err, "phone", cmd.Phone)
	}

	// Unassign agent from Bird Inbox (bot restarts, keep item open)
	p.birdClient.UnassignFeedItem(sess.ConversationID, false)

	p.birdClient.SendText(sess.PhoneNumber, sess.ConversationID, "Hemos retomado tu atencion. Vamos a comenzar de nuevo.")

	// Trigger GREETING handler
	virtualMsg := bird.InboundMessage{
		ID:          fmt.Sprintf("agent-reset-%s-%d", cmd.Phone, time.Now().UnixNano()),
		Phone:       sess.PhoneNumber,
		MessageType: "text",
		ReceivedAt:  time.Now(),
	}
	result, err := p.machine.Process(ctx, sess, virtualMsg)
	if err == nil && result != nil {
		p.sendAndSave(ctx, sess, sess.PhoneNumber, result)
	}

	if p.tracker != nil {
		p.tracker.LogEvent(ctx, sess.ID, cmd.Phone, "escalation_ended", map[string]interface{}{
			"resumed_at_state": statemachine.StateGreeting,
			"by":               "agent_reset",
		})
	}
}

// handleAgentClose closes the session (status=completed).
func (p *MessageWorkerPool) handleAgentClose(ctx context.Context, sess *session.Session, cmd AgentCommand) {
	if err := p.sessionManager.Complete(ctx, sess); err != nil {
		slog.Error("agent close failed", "error", err, "phone", cmd.Phone)
		return
	}

	// Unassign agent + close feed item in Bird Inbox
	p.birdClient.UnassignFeedItem(sess.ConversationID, true)

	p.birdClient.SendText(sess.PhoneNumber, sess.ConversationID, "Tu consulta ha sido resuelta. Gracias por comunicarte con nosotros!")

	if p.tracker != nil {
		p.tracker.LogEvent(ctx, sess.ID, cmd.Phone, "escalation_closed", nil)
	}
}

// handleAgentInfo sends a session context summary (visible to the agent in Bird Inbox).
func (p *MessageWorkerPool) handleAgentInfo(ctx context.Context, sess *session.Session) {
	info := fmt.Sprintf("Info de sesion\n\n"+
		"ID: %s\n"+
		"Estado: %s\n"+
		"Status: %s\n"+
		"Paciente: %s\n"+
		"Documento: %s\n"+
		"Menu: %s\n"+
		"CUPS: %s\n"+
		"Servicio: %s\n"+
		"Equipo: %s\n"+
		"Estado pre-escalacion: %s",
		sess.ID,
		sess.CurrentState,
		sess.Status,
		sess.GetContext("patient_name"),
		sess.GetContext("patient_doc"),
		sess.GetContext("menu_option"),
		sess.GetContext("cups_code"),
		sess.GetContext("service_name"),
		sess.GetContext("escalation_team"),
		sess.GetContext("pre_escalation_state"),
	)
	p.birdClient.SendInternalText(sess.ConversationID, info)
}

// handleAgentOrder processes /bot orden commands — extracts CUPS from a text description using OCR AI.
func (p *MessageWorkerPool) handleAgentOrder(ctx context.Context, sess *session.Session, cmd AgentCommand) {
	if cmd.Data == "" {
		p.birdClient.SendInternalText(sess.ConversationID,
			"Uso: /bot orden <descripcion de la orden>\n"+
				"Ej: /bot orden Resonancia cerebral simple codigo 883141 cantidad 1\n"+
				"Ej: /bot orden Electromiografia 4 ext codigo 930810 cantidad 1, Resonancia columna lumbar codigo 883210 cantidad 1")
		return
	}

	if p.ocrService == nil {
		p.birdClient.SendInternalText(sess.ConversationID, "Error: servicio OCR no disponible.")
		return
	}

	// 1. Call OCR service with text description
	result, err := p.ocrService.AnalyzeText(ctx, cmd.Data)
	if err != nil || !result.Success {
		errMsg := "No se pudieron extraer procedimientos de la descripcion."
		if err != nil {
			errMsg += " Error: " + err.Error()
		} else if result != nil && result.Error != "" {
			errMsg += " Detalle: " + result.Error
		}
		p.birdClient.SendInternalText(sess.ConversationID, errMsg)
		return
	}

	// 2. Serialize CUPS to JSON and store in session
	cupsJSON, _ := json.Marshal(result.Cups)
	sess.SetContext("ocr_cups_json", string(cupsJSON))

	// 3. Resume at VALIDATE_OCR (same flow as image OCR success)
	if err := p.sessionManager.ResumeFromEscalation(ctx, sess, statemachine.StateValidateOCR); err != nil {
		slog.Error("orden resume failed", "error", err, "phone", cmd.Phone)
		p.birdClient.SendInternalText(sess.ConversationID, "Error al retomar sesion: "+err.Error())
		return
	}
	p.birdClient.UnassignFeedItem(sess.ConversationID, false)

	// 4. Send confirmation to agent (internal only)
	var summary strings.Builder
	summary.WriteString("Procedimientos extraidos:\n")
	for _, c := range result.Cups {
		fmt.Fprintf(&summary, "- %s: %s (x%d)\n", c.Code, c.Name, c.Quantity)
	}
	summary.WriteString("\nProcesando con el paciente...")
	p.birdClient.SendInternalText(sess.ConversationID, summary.String())

	// 5. Notify patient and trigger VALIDATE_OCR
	p.birdClient.SendText(sess.PhoneNumber, sess.ConversationID,
		"Hemos retomado tu atencion. Verificando tu orden medica...")

	virtualMsg := bird.InboundMessage{
		ID:          fmt.Sprintf("agent-orden-%s-%d", cmd.Phone, time.Now().UnixNano()),
		Phone:       sess.PhoneNumber,
		MessageType: "text",
		ReceivedAt:  time.Now(),
	}
	r, err := p.machine.Process(ctx, sess, virtualMsg)
	if err == nil && r != nil {
		p.sendAndSave(ctx, sess, sess.PhoneNumber, r)
	}

	if p.tracker != nil {
		p.tracker.LogEvent(ctx, sess.ID, cmd.Phone, "agent_orden_processed", map[string]interface{}{
			"cups_count":  len(result.Cups),
			"description": cmd.Data,
		})
	}
}

// sendAndSave sends result messages and persists state (shared between processMessage and agent commands).
func (p *MessageWorkerPool) sendAndSave(ctx context.Context, sess *session.Session, phone string, result *statemachine.StateResult) {
	// Send messages — route via Conversations API when conversationID is available
	convID := sess.ConversationID
	for _, outMsg := range result.Messages {
		birdMsgID, err := p.sendMessage(phone, convID, outMsg)
		if err != nil {
			slog.Error("send message error", "phone", phone, "error", err)
			continue
		}
		if p.tracker != nil {
			p.tracker.LogMessageSent(ctx, sess.ID, phone, outMsg.Type(), birdMsgID)
		}
	}

	// Persist state + context
	clearAll := false
	for _, k := range result.ClearCtx {
		if k == "__all__" {
			clearAll = true
			break
		}
	}

	if clearAll {
		if err := p.sessionManager.ClearAllContext(ctx, sess); err != nil {
			slog.Error("clear all context error", "phone", phone, "error", err)
		}
		result.ClearCtx = nil
	}

	if err := p.sessionManager.SaveState(ctx, sess, result.NextState, result.UpdateCtx, result.ClearCtx); err != nil {
		slog.Error("save state error", "phone", phone, "error", err)
	}

	// Log events
	if p.tracker != nil && len(result.Events) > 0 {
		p.tracker.LogBatch(ctx, sess.ID, phone, result.Events)
	}
	for _, event := range result.Events {
		slog.Info("event",
			"session_id", sess.ID,
			"phone", phone,
			"type", event.Type,
			"data", event.Data,
		)
	}
}

// sendMessage envía un mensaje saliente vía Bird API.
// Routes via Conversations API when conversationID is available.
func (p *MessageWorkerPool) sendMessage(phone, conversationID string, msg statemachine.OutboundMessage) (string, error) {
	switch m := msg.(type) {
	case *statemachine.TextMessage:
		return p.birdClient.SendText(phone, conversationID, m.Text)
	case *statemachine.ButtonMessage:
		buttons := make([]bird.Button, len(m.Buttons))
		for i, b := range m.Buttons {
			buttons[i] = bird.Button{Text: b.Text, Payload: b.Payload}
		}
		return p.birdClient.SendButtons(phone, conversationID, m.Text, buttons)
	case *statemachine.ListMessage:
		sections := make([]bird.ListSection, len(m.Sections))
		for i, s := range m.Sections {
			rows := make([]bird.ListRow, len(s.Rows))
			for j, r := range s.Rows {
				rows[j] = bird.ListRow{ID: r.ID, Title: r.Title, Description: r.Description}
			}
			sections[i] = bird.ListSection{Title: s.Title, Rows: rows}
		}
		return p.birdClient.SendList(phone, conversationID, m.Body, m.Title, sections)
	default:
		return "", fmt.Errorf("unknown message type: %T", msg)
	}
}

// UpdateConversationID persists a conversationID to the active session for a phone.
// Called from webhook handlers when conversation.created or outbound events arrive.
func (p *MessageWorkerPool) UpdateConversationID(phone, conversationID string) {
	if err := p.sessionManager.UpdateConversationID(p.ctx, phone, conversationID); err != nil {
		slog.Warn("update_conversation_id_failed",
			"phone", phone,
			"conversation_id", conversationID,
			"error", err,
		)
	}
}

// EnqueueVirtual enqueues a virtual trigger message for the waiting list flow.
// The session already exists with SEARCH_SLOTS state, so we just need to trigger processing.
func (p *MessageWorkerPool) EnqueueVirtual(phone string) {
	msg := bird.InboundMessage{
		ID:          fmt.Sprintf("virtual-%s-%d", phone, time.Now().UnixNano()),
		Phone:       phone,
		MessageType: "text",
		Text:        "__waiting_list_trigger__",
		ReceivedAt:  time.Now(),
	}

	select {
	case p.queue <- msg:
		slog.Info("virtual message enqueued", "phone", phone)
	default:
		// Overflow processing — tracked with WaitGroup
		p.activeOverflow.Add(1)
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer p.activeOverflow.Add(-1)
			p.processMessage(p.ctx, msg)
		}()
	}
}

func (p *MessageWorkerPool) startDedupCleanup(ctx context.Context) {
	ticker := time.NewTicker(dedupCleanup)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			p.recentMessages.Range(func(key, value interface{}) bool {
				if now.Sub(value.(time.Time)) > dedupTTL {
					p.recentMessages.Delete(key)
				}
				return true
			})
		}
	}
}
