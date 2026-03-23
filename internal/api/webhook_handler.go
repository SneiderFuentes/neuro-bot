package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/bird"
	"github.com/neuro-bot/neuro-bot/internal/config"
	"github.com/neuro-bot/neuro-bot/internal/notifications"
	"github.com/neuro-bot/neuro-bot/internal/worker"
)

// InboxPersister abstracts message inbox operations for crash recovery (WAL pattern).
type InboxPersister interface {
	InsertIfNotExists(ctx context.Context, id, phone, rawBody, msgType string, receivedAt time.Time) (bool, error)
}

type WebhookHandler struct {
	birdClient    *bird.Client
	workerPool    *worker.MessageWorkerPool
	notifyManager *notifications.NotificationManager
	cfg           *config.Config
	inboxRepo     InboxPersister // WAL for crash recovery (optional)
}

func NewWebhookHandler(birdClient *bird.Client, workerPool *worker.MessageWorkerPool, notifyManager *notifications.NotificationManager, cfg *config.Config) *WebhookHandler {
	return &WebhookHandler{
		birdClient:    birdClient,
		workerPool:    workerPool,
		notifyManager: notifyManager,
		cfg:           cfg,
	}
}

// SetInboxRepo injects the message inbox for crash-recovery persistence (WAL pattern).
func (h *WebhookHandler) SetInboxRepo(repo InboxPersister) {
	h.inboxRepo = repo
}

// HandleWhatsApp procesa webhooks de mensajes inbound de Bird
func (h *WebhookHandler) HandleWhatsApp(w http.ResponseWriter, r *http.Request) {
	body, event, ok := h.verifyAndParse(w, r, false)
	if !ok {
		return
	}

	// Ignorar outbound que lleguen a este endpoint (por si acaso)
	// Bird usa "incoming"/"outgoing" como valores de direction
	if event.Payload.Direction == "outbound" || event.Payload.Direction == "outgoing" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parsear mensaje inbound
	msg := bird.ParseInboundMessage(event)

	// Testing whitelist: ignorar teléfonos no autorizados
	if !h.cfg.IsPhoneWhitelisted(msg.Phone) {
		slog.Debug("phone not whitelisted, ignoring", "phone", msg.Phone)
		w.WriteHeader(http.StatusOK)
		return
	}

	// WAL: persistir mensaje a DB ANTES de responder 200 a Bird.
	// Si el bot crashea después de esto, el mensaje se replayea al reiniciar.
	if h.inboxRepo != nil && msg.ID != "" {
		inserted, err := h.inboxRepo.InsertIfNotExists(r.Context(), msg.ID, msg.Phone, string(body), msg.MessageType, msg.ReceivedAt)
		if err != nil {
			slog.Error("inbox persist failed", "id", msg.ID, "error", err)
			// DB falla → responder 500 → Bird reintenta (fail-safe)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !inserted {
			// Duplicado: ya persistido (quizás Bird reintentó). Acknowledge sin encolar.
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// Mensaje seguro en DB → responder 200 a Bird
	w.WriteHeader(http.StatusOK)

	// Clasificar: postback de notificación o mensaje de chatbot?
	if msg.IsPostback && isNotificationPostback(msg.PostbackPayload) && h.notifyManager != nil && h.notifyManager.HasPending(msg.Phone) {
		slog.Info("notification postback received",
			"phone", msg.Phone,
			"payload", msg.PostbackPayload,
		)
		go h.notifyManager.HandleResponse(msg.Phone, msg.PostbackPayload, msg.ConversationID)
		return
	}

	// Mensaje normal -> Worker pool (state machine)
	h.workerPool.Enqueue(msg)
}

// HandleWhatsAppOutbound procesa webhooks de mensajes outbound de Bird.
// Endpoint separado porque Bird solo permite un tipo de evento por webhook.
// Detecta comandos /bot escritos por agentes humanos en Bird Inbox.
func (h *WebhookHandler) HandleWhatsAppOutbound(w http.ResponseWriter, r *http.Request) {
	_, event, ok := h.verifyAndParse(w, r, true)
	if !ok {
		return
	}

	// Responder 200 inmediatamente (outbound no necesita WAL)
	w.WriteHeader(http.StatusOK)

	// Solo procesar outbound (Bird usa "outgoing")
	if event.Payload.Direction != "outbound" && event.Payload.Direction != "outgoing" {
		return
	}

	h.handleOutbound(event)
}

// verifyAndParse lee el body, verifica HMAC y parsea el evento.
// NO escribe la respuesta HTTP — cada caller decide cuándo responder 200.
// Esto permite a HandleWhatsApp persistir el mensaje a DB antes de responder (WAL pattern).
func (h *WebhookHandler) verifyAndParse(w http.ResponseWriter, r *http.Request, outbound bool) ([]byte, bird.WebhookEvent, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return nil, bird.WebhookEvent{}, false
	}

	// Bird usa MessageBird-Signature + MessageBird-Request-Timestamp
	// Firma = HMAC-SHA256(signingKey, timestamp + "\n" + url + "\n" + SHA256(body)), base64
	signature := r.Header.Get("MessageBird-Signature")
	timestamp := r.Header.Get("MessageBird-Request-Timestamp")

	// Bird firma con la URL completa que tiene configurada.
	// Reconstruir desde X-Forwarded-Host/Proto (ngrok) o Host header.
	requestURL := reconstructFullURL(r)

	// Each Bird webhook subscription has its own signing key
	var valid bool
	if outbound {
		valid = h.birdClient.VerifyOutboundWebhookSignature(signature, timestamp, requestURL, body)
	} else {
		valid = h.birdClient.VerifyWebhookSignature(signature, timestamp, requestURL, body)
	}

	if !valid {
		// For outbound: try inbound key as fallback (Bird may sign agent messages with a different subscription key)
		if outbound {
			valid = h.birdClient.VerifyWebhookSignature(signature, timestamp, requestURL, body)
		}
	}

	if !valid {
		// Log truncated body to identify the source of unknown events
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		slog.Warn("invalid webhook signature",
			"has_signature", signature != "",
			"has_timestamp", timestamp != "",
			"url", requestURL,
			"body_len", len(body),
			"outbound", outbound,
			"body_preview", preview,
		)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return nil, bird.WebhookEvent{}, false
	}

	var event bird.WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		slog.Error("parse webhook event", "error", err)
		return body, bird.WebhookEvent{}, false
	}

	return body, event, true
}

// handleOutbound processes outbound webhook events.
// 1. Caches conversationId for future escalation (from ALL outbound messages).
// 2. Detects /bot commands from agents in Bird Inbox.
func (h *WebhookHandler) handleOutbound(event bird.WebhookEvent) {
	// Extract phone from receiver (Bird uses "connector" singular, legacy uses "contacts" array)
	phone := ""
	if event.Payload.Receiver.Connector.IdentifierValue != "" {
		phone = event.Payload.Receiver.Connector.IdentifierValue
	} else if len(event.Payload.Receiver.Contacts) > 0 {
		phone = event.Payload.Receiver.Contacts[0].IdentifierValue
	}

	slog.Debug("outbound_event_received",
		"phone", phone,
		"conversation_id", event.Payload.ConversationID,
		"direction", event.Payload.Direction,
		"status", event.Payload.Status,
		"msg_id", event.Payload.ID,
		"body_type", event.Payload.Body.Type,
	)

	// Cache conversationId from ALL outbound messages and persist to DB
	if event.Payload.ConversationID != "" && phone != "" {
		h.birdClient.CacheConversationID(phone, event.Payload.ConversationID)
		h.workerPool.UpdateConversationID(phone, event.Payload.ConversationID)
		slog.Debug("outbound_conversation_cached",
			"phone", phone,
			"conversation_id", event.Payload.ConversationID,
		)
	}

	// Check for agent /bot command
	// Bird Inbox agent messages arrive WITHOUT body in the webhook — fetch via API
	text := ""
	if event.Payload.Body.Text.Text != "" {
		text = event.Payload.Body.Text.Text
	} else if event.Payload.ID != "" {
		text = h.birdClient.FetchMessageText(event.Payload.ID)
	}
	text = strings.TrimSpace(text)

	if !strings.HasPrefix(text, "/bot") {
		return // Not an agent command — ignore
	}

	if phone == "" {
		slog.Warn("outbound /bot command without phone", "text", text)
		return
	}

	cmd := worker.ParseAgentCommand(text)
	cmd.Phone = phone

	slog.Info("agent command received",
		"phone", phone,
		"action", cmd.Action,
		"state", cmd.State,
		"data", cmd.Data,
	)

	h.workerPool.EnqueueAgentCommand(cmd)
}

// HandleConversation procesa webhooks del servicio Conversations de Bird.
// Cachea phone → conversationId cuando se crea una conversación.
func (h *WebhookHandler) HandleConversation(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// Verify signature only if a separate conversations webhook secret is configured.
	// If not configured, skip validation (conversations webhook only caches conversation IDs).
	if h.cfg.BirdWebhookSecretConversations != "" {
		signature := r.Header.Get("MessageBird-Signature")
		timestamp := r.Header.Get("MessageBird-Request-Timestamp")
		requestURL := reconstructFullURL(r)
		if !bird.VerifySignatureWithKey(h.cfg.BirdWebhookSecretConversations, signature, timestamp, requestURL, body) {
			slog.Warn("invalid conversation webhook signature",
				"has_signature", signature != "",
				"has_timestamp", timestamp != "",
				"url", requestURL,
			)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	w.WriteHeader(http.StatusOK)

	var event bird.ConversationEvent
	if err := json.Unmarshal(body, &event); err != nil {
		slog.Error("parse conversation webhook", "error", err)
		return
	}

	if event.Event != "conversation.created" {
		return
	}

	convID := event.Payload.ID
	if convID == "" {
		return
	}

	// Extract phone (Bird may use identifierValue directly or nested in contact)
	phone := ""
	for _, p := range event.Payload.FeaturedParticipants {
		if p.IdentifierValue != "" {
			phone = p.IdentifierValue
			break
		}
		if p.Contact.IdentifierValue != "" {
			phone = p.Contact.IdentifierValue
			break
		}
	}

	if phone != "" {
		h.birdClient.CacheConversationID(phone, convID)
		h.workerPool.UpdateConversationID(phone, convID)
		slog.Info("conversation_created_cached",
			"phone", phone,
			"conversation_id", convID,
			"channel_id", event.Payload.ChannelID,
		)
	} else {
		slog.Debug("conversation_created_no_phone",
			"conversation_id", convID,
			"raw", string(body),
		)
	}
}

// reconstructFullURL reconstruye la URL completa que Bird usó para firmar.
// Usa X-Forwarded-Proto/Host (ngrok) o Host header como fallback.
func reconstructFullURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host + r.URL.String()
}

// isNotificationPostback determina si un postback viene de un template proactivo
func isNotificationPostback(payload string) bool {
	switch payload {
	case "confirm", "cancelar", "cancel", "understood", "reschedule", "reprogramar",
		"wl_schedule", "wl_decline":
		return true
	default:
		return false
	}
}
