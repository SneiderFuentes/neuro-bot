package bird

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/neuro-bot/neuro-bot/internal/config"
)

// ErrConversationNotActive is returned when the Conversations API rejects a
// request because the conversation is closed/inactive (HTTP 422).
var ErrConversationNotActive = errors.New("conversation not active")

type Client struct {
	httpClient  *http.Client
	apiURL      string
	apiKeyWA    string
	accessKeyID string
	workspaceID string
	channelID   string
	// Templates
	channelIDTemplates string
	// Webhook
	WebhookSecret         string
	WebhookSecretOutbound string // Separate key for outbound webhook (Bird issues unique keys per subscription)
	// Conversations API base URL (different from Channels API)
	conversationsAPIURL string
	// Voice
	voiceChannelID  string
	voiceNumber     string
	voiceAPIKey     string
	voiceFlowID     string // Bird Flow ID for IVR (has webhook step for DTMF result)
	voiceWebhookURL string // notification URL for voice events (e.g. https://server/webhooks/voice)
	// ConversationID cache: phone → conversationID (from conversation.created webhook).
	// Self-replacing: new conversation.created overwrites the old entry automatically.
	mu        sync.RWMutex
	convCache map[string]string
}

func NewClient(cfg *config.Config) *Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		httpClient:          &http.Client{Timeout: 30 * time.Second, Transport: transport},
		apiURL:              cfg.BirdAPIURL,
		apiKeyWA:            cfg.BirdAPIKeyWA,
		accessKeyID:         cfg.BirdAccessKeyID,
		workspaceID:         cfg.BirdWorkspaceID,
		channelID:           cfg.BirdChannelID,
		channelIDTemplates:  cfg.BirdChannelIDTemplates,
		WebhookSecret:         cfg.BirdWebhookSecret,
		WebhookSecretOutbound: cfg.ResolveOutboundWebhookSecret(),
		voiceChannelID:  cfg.BirdVoiceChannelID,
		voiceNumber:     cfg.BirdVoiceNumber,
		voiceAPIKey:     cfg.BirdAPIKeyVoice,
		voiceFlowID:     cfg.BirdVoiceFlowID,
		voiceWebhookURL: voiceNotificationURL(cfg),
		convCache: make(map[string]string),
	}
}

// NewClientForTest creates a Client pointing at a custom base URL (for httptest).
func NewClientForTest(baseURL string) *Client {
	return &Client{
		httpClient:          &http.Client{Timeout: 5 * time.Second},
		apiURL:              baseURL,
		conversationsAPIURL: baseURL,
		workspaceID:         "ws-test",
		channelID:           "ch-test",
		convCache:           make(map[string]string),
	}
}

// CacheConversationID stores the conversationID for a phone (from conversation.created webhook).
// Self-replacing: a new conversation.created webhook overwrites the previous entry.
func (c *Client) CacheConversationID(phone, conversationID string) {
	c.mu.Lock()
	c.convCache[phone] = conversationID
	c.mu.Unlock()
}

// GetCachedConversationID returns the cached conversationID for a phone, or "" if not found.
func (c *Client) GetCachedConversationID(phone string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.convCache[phone]
}

// conversationsBase returns the base URL for the Conversations/Collaborations API.
// This is different from apiURL which points to the Channels API.
func (c *Client) conversationsBase() string {
	if c.conversationsAPIURL != "" {
		return c.conversationsAPIURL
	}
	return "https://api.bird.com"
}

// MarkConversationEscalated updates the conversation name/description in Bird Inbox
// to signal escalation to agents. This is visible to agents in the inbox.
func (c *Client) MarkConversationEscalated(conversationID, teamName, patientName string) error {
	if conversationID == "" {
		return nil
	}

	url := fmt.Sprintf("%s/workspaces/%s/conversations/%s",
		c.conversationsBase(), c.workspaceID, conversationID)

	name := fmt.Sprintf("ESCALACION - %s - %s", teamName, patientName)
	payload := map[string]interface{}{
		"name": name,
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal conversation update: %w", err)
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create conversation update request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update conversation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("mark conversation escalated failed",
			"status", resp.StatusCode, "body", string(respBody))
	}

	return nil
}

// sendToConversation sends a message body via the Conversations API.
// Messages sent this way appear in Bird Inbox. When draft=true the message
// is visible only in Inbox and NOT delivered to WhatsApp.
// Returns ErrConversationNotActive when the conversation is closed (422).
func (c *Client) sendToConversation(conversationID string, body interface{}, draft bool) (string, error) {
	url := fmt.Sprintf("%s/workspaces/%s/conversations/%s/messages",
		c.conversationsBase(), c.workspaceID, conversationID)
	payload := map[string]interface{}{
		"participantId":   c.channelID,
		"participantType": "flow",
		"body":            body,
	}
	if draft {
		payload["draft"] = true
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal conversation message: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create conversation message request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.apiKeyWA)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send conversation message: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	slog.Debug("conversations_api_response", "status", resp.StatusCode, "conversation_id", conversationID, "body_len", len(respBody))

	if resp.StatusCode == 422 {
		return "", fmt.Errorf("%w: %s", ErrConversationNotActive, string(respBody))
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("conversations api: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		slog.Debug("could not parse conversation response", "body", string(respBody))
	}
	return result.ID, nil
}

// messagesURL construye la URL base para envío de mensajes
func (c *Client) messagesURL() string {
	base := c.apiURL
	if base == "" {
		base = "https://api.bird.com"
	}
	// If apiURL already contains the full channel path (e.g. BIRD_API_URL=https://api.bird.com/workspaces/{id}/channels/{id}),
	// just append /messages instead of doubling the workspace/channel segments.
	if strings.Contains(base, "/channels/") {
		return base + "/messages"
	}
	return fmt.Sprintf("%s/workspaces/%s/channels/%s/messages", base, c.workspaceID, c.channelID)
}

// templatesURL construye la URL para envío de templates (puede usar otro channelID)
func (c *Client) templatesURL() string {
	chID := c.channelIDTemplates
	if chID == "" {
		chID = c.channelID
	}
	base := c.apiURL
	if base == "" {
		base = "https://api.bird.com"
	}
	// If apiURL already contains the full channel path, swap the channelID when needed.
	if strings.Contains(base, "/channels/") {
		if chID != c.channelID && c.channelID != "" {
			base = strings.Replace(base, c.channelID, chID, 1)
		}
		return base + "/messages"
	}
	return fmt.Sprintf("%s/workspaces/%s/channels/%s/messages", base, c.workspaceID, chID)
}

// FetchMessageText retrieves the text content of a message by ID.
// Used when outbound webhooks arrive without body (agent messages from Bird Inbox).
func (c *Client) FetchMessageText(messageID string) string {
	if messageID == "" {
		return ""
	}
	url := fmt.Sprintf("%s/%s", c.messagesURL(), messageID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "AccessKey "+c.apiKeyWA)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Warn("fetch_message_text_failed", "error", err, "message_id", messageID)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("fetch_message_text_status", "status", resp.StatusCode, "message_id", messageID)
		return ""
	}

	var result struct {
		Body struct {
			Type string `json:"type"`
			Text struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.Body.Text.Text
}

// trySendToConversation attempts to send via Conversations API.
// On 422 (conversation not active), it looks up the current conversation by phone,
// updates the cache, and retries once with the fresh ID.
// Returns (msgID, error, sent) — sent=true means Conversations API succeeded.
func (c *Client) trySendToConversation(phone, conversationID string, body interface{}) (string, error, bool) {
	if conversationID == "" {
		return "", nil, false
	}

	id, err := c.sendToConversation(conversationID, body, false)
	if err == nil {
		return id, nil, true
	}

	// Self-heal: if the conversation is closed/stuck, look up a fresh one and retry
	if errors.Is(err, ErrConversationNotActive) {
		// Invalidate cached conversation_id so subsequent messages don't hit the same wall
		c.mu.Lock()
		if c.convCache[phone] == conversationID {
			delete(c.convCache, phone)
		}
		c.mu.Unlock()

		if freshID, lookErr := c.LookupConversationByPhone(phone); lookErr == nil && freshID != "" && freshID != conversationID {
			slog.Info("conversation_id_self_healed",
				"phone", phone,
				"old", conversationID,
				"new", freshID,
			)
			c.CacheConversationID(phone, freshID)
			if id2, err2 := c.sendToConversation(freshID, body, false); err2 == nil {
				return id2, nil, true
			}
		}
	}

	slog.Warn("conversations_api_fallback", "error", err, "phone", phone)
	return "", err, false
}

// SendText envía un mensaje de texto simple.
// If conversationID is provided, routes via Conversations API (WhatsApp + Inbox).
// Falls back to Channels API (WhatsApp only) if conversationID is empty or Conversations API fails.
func (c *Client) SendText(to, conversationID, text string) (string, error) {
	body := map[string]interface{}{
		"type": "text",
		"text": map[string]string{"text": text},
	}
	if id, _, ok := c.trySendToConversation(to, conversationID, body); ok {
		return id, nil
	}
	// Fallback: Channels API
	payload := map[string]interface{}{
		"receiver": map[string]interface{}{
			"contacts": []map[string]string{{"identifierValue": to}},
		},
		"body": body,
	}
	return c.sendMessage(c.messagesURL(), payload)
}

// SendButtons envía un mensaje con botones postback (máx 3).
// Routes via Conversations API when conversationID is available.
func (c *Client) SendButtons(to, conversationID, text string, buttons []Button) (string, error) {
	actions := make([]map[string]interface{}, len(buttons))
	for i, btn := range buttons {
		actions[i] = map[string]interface{}{
			"type": "postback",
			"postback": map[string]string{
				"text":    btn.Text,
				"payload": btn.Payload,
			},
		}
	}

	body := map[string]interface{}{
		"type": "text",
		"text": map[string]interface{}{
			"text":    text,
			"actions": actions,
		},
	}
	// If no conversationID, try a fresh lookup before falling back to text
	if conversationID == "" {
		if fresh, err := c.LookupConversationByPhone(to); err == nil && fresh != "" {
			conversationID = fresh
			c.CacheConversationID(to, fresh)
		}
	}
	if id, _, ok := c.trySendToConversation(to, conversationID, body); ok {
		return id, nil
	}
	// Fallback: Channels API — interactive buttons not supported, send as numbered text
	slog.Info("send_buttons_text_fallback", "phone", to, "buttons", len(buttons))
	var textFallback string
	textFallback = text + "\n"
	for i, btn := range buttons {
		textFallback += fmt.Sprintf("\n%d. %s", i+1, btn.Text)
	}
	return c.SendText(to, "", textFallback)
}

// SendList envía un mensaje con lista interactiva.
// Routes via Conversations API when conversationID is available.
func (c *Client) SendList(to, conversationID, body, buttonLabel string, sections []ListSection) (string, error) {
	totalRows := 0
	for _, s := range sections {
		totalRows += len(s.Rows)
	}
	slog.Debug("send_list_building", "phone", to, "sections", len(sections), "total_rows", totalRows, "button", buttonLabel)

	items := make([]map[string]interface{}, len(sections))
	for i, section := range sections {
		actions := make([]map[string]interface{}, len(section.Rows))
		for j, row := range section.Rows {
			actions[j] = map[string]interface{}{
				"type": "postback",
				"postback": map[string]string{
					"text":    row.Title,
					"payload": row.ID,
				},
			}
		}
		items[i] = map[string]interface{}{
			"title":   section.Title,
			"actions": actions,
		}
	}

	msgBody := map[string]interface{}{
		"type": "list",
		"list": map[string]interface{}{
			"text":  body,
			"title": buttonLabel,
			"items": items,
			"metadata": map[string]interface{}{
				"button": map[string]string{"label": buttonLabel},
			},
		},
	}

	// Debug: log the full payload for list messages
	if debugJSON, err := json.Marshal(msgBody); err == nil {
		slog.Debug("send_list_payload", "phone", to, "payload", string(debugJSON))
	}

	if id, _, ok := c.trySendToConversation(to, conversationID, msgBody); ok {
		return id, nil
	}
	// Fallback: Channels API — interactive lists not supported, send as numbered text
	slog.Info("send_list_text_fallback", "phone", to, "sections", len(sections))
	var textFallback string
	textFallback = body + "\n"
	idx := 1
	for _, section := range sections {
		for _, row := range section.Rows {
			desc := ""
			if row.Description != "" {
				desc = " — " + row.Description
			}
			textFallback += fmt.Sprintf("\n%d. %s%s", idx, row.Title, desc)
			idx++
		}
	}
	return c.SendText(to, "", textFallback)
}

// SendInternalText sends a text message visible only in Bird Inbox (not delivered to WhatsApp).
// Uses Conversations API with draft:true. Returns ("", nil) if no conversationID.
func (c *Client) SendInternalText(conversationID, text string) (string, error) {
	if conversationID == "" {
		return "", nil
	}
	body := map[string]interface{}{
		"type": "text",
		"text": map[string]string{"text": text},
	}
	return c.sendToConversation(conversationID, body, true)
}

// SendTemplate envía un template de WhatsApp aprobado (HSM)
func (c *Client) SendTemplate(to string, tmpl TemplateConfig) (string, error) {
	params := make([]map[string]string, len(tmpl.Params))
	for i, p := range tmpl.Params {
		params[i] = map[string]string{
			"type":  "string",
			"key":   p.Key,
			"value": p.Value,
		}
	}

	version := tmpl.VersionID
	if version == "" {
		version = "latest"
	}

	payload := map[string]interface{}{
		"receiver": map[string]interface{}{
			"contacts": []map[string]string{
				{"identifierValue": to},
			},
		},
		"template": map[string]interface{}{
			"projectId":  tmpl.ProjectID,
			"version":    version,
			"locale":     tmpl.Locale,
			"parameters": params,
		},
	}

	if debugJSON, err := json.Marshal(payload); err == nil {
		slog.Debug("send_template_payload", "to", to, "payload", string(debugJSON))
	}

	return c.sendMessage(c.templatesURL(), payload)
}

// voiceNotificationURL builds the voice event notification URL.
// Prefers SERVER_PUBLIC_URL; falls back to NGROK_HOSTNAME.
func voiceNotificationURL(cfg *config.Config) string {
	base := cfg.ServerPublicURL
	if base == "" && cfg.NgrokHostname != "" {
		base = "https://" + cfg.NgrokHostname
	}
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/api/webhooks/voice"
}

// PlaceCall inicia una llamada IVR via Bird Voice API.
// Params esperados: patient_name, appointment_date, appointment_time, clinic_name, clinic_address.
// Retorna el callId de Bird para correlacionar con el webhook DTMF posterior.
func (c *Client) PlaceCall(to string, params map[string]string) (string, error) {
	if c.voiceChannelID == "" {
		return "", fmt.Errorf("voice channel not configured (BIRD_VOICE_CHANNEL_ID missing)")
	}

	var greeting string
	if addr := params["clinic_address"]; addr != "" {
		greeting = fmt.Sprintf(
			"Hola %s, te hablamos desde %s para recordarte tu cita el día %s a las %s en la dirección %s.",
			params["patient_name"], params["clinic_name"],
			params["appointment_date"], params["appointment_time"], addr,
		)
	} else {
		greeting = fmt.Sprintf(
			"Hola %s, te hablamos desde %s para recordarte tu cita el día %s a las %s.",
			params["patient_name"], params["clinic_name"],
			params["appointment_date"], params["appointment_time"],
		)
	}

	// Build call payload.
	// If a Bird Flow ID is configured, use it (the flow has a webhook step that POSTs
	// DTMF results to our server). Otherwise fall back to inline callFlow (greeting +
	// gather only — no DTMF result delivery, but still plays reminder to patient).
	payload := map[string]interface{}{
		"to":          to,
		"from":        c.voiceNumber,
		"maxDuration": 120,
		"ringTimeout": 30,
		"record":      true,
		"recordStart": "record-from-answer",
	}

	if c.voiceFlowID != "" {
		// Bird Flow mode: pass flow ID + dynamic variables for TTS substitution.
		// The flow must be configured in the Bird dashboard with a webhook step that
		// POSTs DTMF results to /api/webhooks/voice/dtmf on our server.
		payload["flowId"] = c.voiceFlowID
		payload["variables"] = map[string]string{
			"patient_name":     params["patient_name"],
			"clinic_name":      params["clinic_name"],
			"appointment_date": params["appointment_date"],
			"appointment_time": params["appointment_time"],
			"clinic_address":   params["clinic_address"],
		}
		slog.Info("voice call using Bird Flow", "flowId", c.voiceFlowID)
	} else {
		// Inline callFlow fallback: greeting + gather.
		// DTMF result is NOT delivered to the server (Bird API limitation).
		// Call still plays the reminder and gather prompt to the patient.
		callFlow := []map[string]interface{}{
			{
				"command": "say",
				"options": map[string]interface{}{
					"locale": "es-MX", "voice": "female", "text": greeting,
				},
			},
			{
				"command": "gather",
				"options": map[string]interface{}{
					"maxNumKeys": 1,
					"timeout":    10,
					"retries":    4,
					"input":      "dtmf",
					"say": map[string]interface{}{
						"locale": "es-MX", "voice": "female",
						"text": "Para confirmar su cita, oprima 1. Para cancelar, oprima 2.",
					},
				},
			},
			{"command": "hangup"},
		}
		payload["callFlow"] = callFlow
		slog.Warn("voice call using inline callFlow (no DTMF result) — set BIRD_VOICE_FLOW_ID to enable DTMF webhook")
	}

	if c.voiceWebhookURL != "" {
		payload["notification"] = map[string]interface{}{
			"url": c.voiceWebhookURL,
		}
		slog.Info("voice call notification URL set", "url", c.voiceWebhookURL)
	} else {
		slog.Warn("voice call placed WITHOUT notification URL — DTMF webhook will not be received")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal voice call payload: %w", err)
	}

	slog.Debug("voice call payload", "body", string(body))

	url := fmt.Sprintf("%s/workspaces/%s/channels/%s/calls",
		c.conversationsBase(), c.workspaceID, c.voiceChannelID)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create voice call request: %w", err)
	}
	authKey := c.voiceAPIKey
	if authKey == "" {
		authKey = c.accessKeyID
	}
	req.Header.Set("Authorization", "AccessKey "+authKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("voice call http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("voice call API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse voice call response: %w", err)
	}

	slog.Info("voice call placed", "to", to, "callId", result.ID)
	return result.ID, nil
}

// SendGather sends a mid-call DTMF gather command to an already-active call.
// Called when the voice.outbound webhook reports status="ongoing".
// Returns the gather commandId, which can later be queried via GetGatherResult.
func (c *Client) SendGather(callID string) (commandID string, err error) {
	if c.voiceChannelID == "" {
		return "", fmt.Errorf("voice channel not configured")
	}

	payload := map[string]interface{}{
		"maxNumKeys": 1,
		"timeout":    10,
		"retries":    4,
		"input":      "dtmf",
		"say": map[string]interface{}{
			"locale": "es-MX",
			"voice":  "female",
			"text":   "Para confirmar su cita, oprima 1. Para cancelar, oprima 2.",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal gather payload: %w", err)
	}

	url := fmt.Sprintf("%s/workspaces/%s/channels/%s/calls/%s/gather",
		c.conversationsBase(), c.workspaceID, c.voiceChannelID, callID)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create gather request: %w", err)
	}
	authKey := c.voiceAPIKey
	if authKey == "" {
		authKey = c.accessKeyID
	}
	req.Header.Set("Authorization", "AccessKey "+authKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gather http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("gather API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	json.Unmarshal(respBody, &result) // best-effort; commandId may be empty

	slog.Info("voice gather sent", "callId", callID, "commandId", result.ID)

	// Schedule hangup after max gather duration: retries(4) × timeout(10s) + 15s buffer.
	// Bird keeps the call alive after gather — we must explicitly hang it up.
	// The completed webhook will then trigger GetGatherResult.
	go func() {
		time.Sleep(55 * time.Second)
		if err := c.HangupCall(callID); err != nil {
			slog.Debug("hangup after gather (may already be closed)", "callId", callID, "error", err)
		}
	}()

	return result.ID, nil
}

// GetGatherResult queries a gather command's result via GET /calls/{callId}/commands/{commandId}.
// Returns the DTMF keys pressed, or "" if the patient did not press anything.
func (c *Client) GetGatherResult(callID, commandID string) (string, error) {
	if commandID == "" {
		return "", fmt.Errorf("commandId is empty")
	}

	url := fmt.Sprintf("%s/workspaces/%s/channels/%s/calls/%s/commands/%s",
		c.conversationsBase(), c.workspaceID, c.voiceChannelID, callID, commandID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create get-command request: %w", err)
	}
	authKey := c.voiceAPIKey
	if authKey == "" {
		authKey = c.accessKeyID
	}
	req.Header.Set("Authorization", "AccessKey "+authKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get command http: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	slog.Info("get gather command result", "callId", callID, "commandId", commandID, "raw", string(body))

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("get command API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Command string `json:"command"`
		Status  string `json:"status"`
		Result  struct {
			Keys string `json:"keys"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse command result: %w", err)
	}
	return result.Result.Keys, nil
}

// HangupCall sends a hangup command to an active call via Bird Voice API.
func (c *Client) HangupCall(callID string) error {
	url := fmt.Sprintf("%s/workspaces/%s/channels/%s/calls/%s/hangup",
		c.conversationsBase(), c.workspaceID, c.voiceChannelID, callID)

	req, err := http.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return fmt.Errorf("create hangup request: %w", err)
	}
	authKey := c.voiceAPIKey
	if authKey == "" {
		authKey = c.accessKeyID
	}
	req.Header.Set("Authorization", "AccessKey "+authKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("hangup http: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) // drain

	if resp.StatusCode >= 400 {
		return fmt.Errorf("hangup API error %d", resp.StatusCode)
	}
	slog.Info("call hung up", "callId", callID)
	return nil
}

// CallDetails holds the relevant fields from Bird's GET /calls/{id} response.
type CallDetails struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Duration int    `json:"duration"`
	// callCommands contains the individual command results (gather keys, etc.)
	CallCommands []struct {
		Command string `json:"command"`
		Status  string `json:"status"`
		Result  struct {
			Keys string `json:"keys"`
		} `json:"result"`
	} `json:"callCommands"`
}

// GetCallDetails fetches the full call object from Bird, including callCommands.
// Used after a call completes to retrieve the gather (DTMF) result.
func (c *Client) GetCallDetails(callID string) (*CallDetails, error) {
	url := fmt.Sprintf("%s/workspaces/%s/channels/%s/calls/%s",
		c.conversationsBase(), c.workspaceID, c.voiceChannelID, callID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create get-call request: %w", err)
	}
	authKey := c.voiceAPIKey
	if authKey == "" {
		authKey = c.accessKeyID
	}
	req.Header.Set("Authorization", "AccessKey "+authKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get call http: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("get call API error %d: %s", resp.StatusCode, string(body))
	}

	// Log the full raw response so we can discover the exact structure Bird returns
	slog.Info("get call details response", "callId", callID, "raw", string(body))

	var details CallDetails
	if err := json.Unmarshal(body, &details); err != nil {
		return nil, fmt.Errorf("parse call details: %w", err)
	}
	return &details, nil
}

// GatherKeysFromCallDetails extracts the DTMF keys from a completed call's command results.
func GatherKeysFromCallDetails(details *CallDetails) string {
	for _, cmd := range details.CallCommands {
		if cmd.Command == "gather" {
			return cmd.Result.Keys
		}
	}
	return ""
}

// ListActiveAgents returns all agents with status "active" from Bird Inbox.
func (c *Client) ListActiveAgents() ([]AgentInfo, error) {
	return c.listAgents("active")
}

// ListAllAgents returns all agents regardless of status from Bird Inbox.
func (c *Client) ListAllAgents() ([]AgentInfo, error) {
	return c.listAgents("")
}

// listAgents fetches agents from Bird Inbox. statusFilter limits by agentStatuses (e.g. "active");
// pass "" to fetch all agents regardless of status.
func (c *Client) listAgents(statusFilter string) ([]AgentInfo, error) {
	reqURL := fmt.Sprintf("%s/workspaces/%s/agents", c.conversationsBase(), c.workspaceID)
	if statusFilter != "" {
		reqURL += "?agentStatuses=" + statusFilter
	}

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create agents request: %w", err)
	}
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("list agents: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var result AgentListResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse agents response: %w", err)
	}
	return result.Results, nil
}

// AssignFeedItem assigns a conversation to a team+agent in Bird Inbox.
// Uses the Feeds API: PATCH /feeds/channel:{channelId}/items/{conversationId}
// If the feed item doesn't exist (404), attempts to create it via POST first.
func (c *Client) AssignFeedItem(conversationID, teamID, agentID string) error {
	if conversationID == "" {
		return nil
	}

	feedID := "channel:" + c.channelID
	url := fmt.Sprintf("%s/workspaces/%s/feeds/%s/items/%s",
		c.conversationsBase(), c.workspaceID, feedID, conversationID)

	payload := map[string]interface{}{
		"teamId": teamID,
	}
	if agentID != "" {
		payload["agentId"] = agentID
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal feed item assignment: %w", err)
	}

	resp, err := c.doPatchFeedItem(url, jsonBody)
	if err != nil {
		return fmt.Errorf("assign feed item: %w", err)
	}

	if resp == 404 {
		// Feed item doesn't exist — create it via POST then retry PATCH
		slog.Info("feed_item_not_found_creating",
			"conversation_id", conversationID,
			"feed_id", feedID,
		)
		if createErr := c.createFeedItem(feedID, conversationID); createErr != nil {
			slog.Warn("create_feed_item_failed", "error", createErr)
			return fmt.Errorf("assign feed item: item not found and create failed: %w", createErr)
		}
		resp, err = c.doPatchFeedItem(url, jsonBody)
		if err != nil {
			return fmt.Errorf("assign feed item (retry): %w", err)
		}
		if resp >= 400 {
			return fmt.Errorf("assign feed item (retry): status %d", resp)
		}
	}
	return nil
}

// doPatchFeedItem sends a PATCH request to the feed items endpoint. Returns status code.
func (c *Client) doPatchFeedItem(url string, jsonBody []byte) (int, error) {
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(jsonBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 404 {
			return 404, nil
		}
		return resp.StatusCode, fmt.Errorf("status %d, body: %s", resp.StatusCode, string(respBody))
	}
	return resp.StatusCode, nil
}

// createFeedItem creates a feed item for a conversation via POST.
func (c *Client) createFeedItem(feedID, conversationID string) error {
	url := fmt.Sprintf("%s/workspaces/%s/feeds/%s/items",
		c.conversationsBase(), c.workspaceID, feedID)

	payload := map[string]interface{}{
		"conversationId": conversationID,
	}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create feed item: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	slog.Info("feed_item_created", "conversation_id", conversationID, "feed_id", feedID)
	return nil
}

// UnassignFeedItem removes agent/team assignment from a feed item in Bird Inbox.
// If closed=true, also closes the item (removes from agent's queue permanently).
// Non-blocking: logs errors but does not fail.
func (c *Client) UnassignFeedItem(conversationID string, closed bool) error {
	if conversationID == "" {
		return nil
	}

	feedID := "channel:" + c.channelID
	url := fmt.Sprintf("%s/workspaces/%s/feeds/%s/items/%s",
		c.conversationsBase(), c.workspaceID, feedID, conversationID)

	payload := map[string]interface{}{
		"agentId": nil,
		"teamId":  nil,
		"closed":  closed,
	}

	jsonBody, _ := json.Marshal(payload)

	req, err := http.NewRequest("PATCH", url, bytes.NewReader(jsonBody))
	if err != nil {
		slog.Warn("unassign_feed_item_request_failed", "error", err, "conversation_id", conversationID)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Warn("unassign_feed_item_failed", "error", err, "conversation_id", conversationID)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("unassign_feed_item_error",
			"status", resp.StatusCode,
			"body", string(respBody),
			"conversation_id", conversationID)
	}
	return nil
}

// CloseFeedItems searches for all feed items tied to a conversation and closes
// the open ones. This is the correct way to mark a ticket as "Cerrado" in Bird
// Inbox (Collaborations API → search/feed-items → PATCH closed:true).
// It finds items across ALL feeds (channel, team, agent) so it works regardless
// of which feed the agents are viewing.
func (c *Client) CloseFeedItems(conversationID string) error {
	if conversationID == "" {
		return nil
	}

	// 1. Search feed items by conversationId
	searchURL := fmt.Sprintf("%s/workspaces/%s/search/feed-items",
		c.conversationsBase(), c.workspaceID)

	searchPayload, _ := json.Marshal(map[string]interface{}{
		"conversationIds": []string{conversationID},
	})

	req, err := http.NewRequest("POST", searchURL, bytes.NewReader(searchPayload))
	if err != nil {
		slog.Warn("close_feed_items_search_request_failed", "error", err, "conversation_id", conversationID)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Warn("close_feed_items_search_failed", "error", err, "conversation_id", conversationID)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("close_feed_items_search_error",
			"status", resp.StatusCode,
			"body", string(respBody),
			"conversation_id", conversationID)
		return fmt.Errorf("search feed items: status %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			ID     string `json:"id"`
			FeedID string `json:"feedId"`
			Closed bool   `json:"closed"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Warn("close_feed_items_decode_error", "error", err, "conversation_id", conversationID)
		return err
	}

	// 2. Close each open feed item
	closed := 0
	for _, item := range result.Results {
		if item.Closed {
			continue
		}

		patchURL := fmt.Sprintf("%s/workspaces/%s/feeds/%s/items/%s",
			c.conversationsBase(), c.workspaceID, item.FeedID, item.ID)

		patchBody, _ := json.Marshal(map[string]interface{}{"closed": true})

		patchReq, err := http.NewRequest("PATCH", patchURL, bytes.NewReader(patchBody))
		if err != nil {
			slog.Warn("close_feed_item_request_failed", "error", err, "item_id", item.ID, "feed_id", item.FeedID)
			continue
		}
		patchReq.Header.Set("Content-Type", "application/json")
		patchReq.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

		patchResp, err := c.httpClient.Do(patchReq)
		if err != nil {
			slog.Warn("close_feed_item_failed", "error", err, "item_id", item.ID, "feed_id", item.FeedID)
			continue
		}
		patchResp.Body.Close()

		if patchResp.StatusCode >= 400 {
			slog.Warn("close_feed_item_error",
				"status", patchResp.StatusCode,
				"item_id", item.ID,
				"feed_id", item.FeedID,
				"conversation_id", conversationID)
		} else {
			closed++
			slog.Info("feed_item_closed",
				"item_id", item.ID,
				"feed_id", item.FeedID,
				"conversation_id", conversationID)
		}
	}

	if closed == 0 && len(result.Results) > 0 {
		slog.Debug("no_open_feed_items_to_close",
			"conversation_id", conversationID,
			"total_items", len(result.Results))
	}

	return nil
}

// pickLeastLoadedAgent filters agents by teamID and returns the least loaded one.
// Prefers agents with activity "available"; falls back to any active agent in the team.
func pickLeastLoadedAgent(agents []AgentInfo, teamID string) *AgentInfo {
	var best *AgentInfo
	var bestFallback *AgentInfo
	for i := range agents {
		a := &agents[i]
		inTeam := false
		for _, t := range a.Teams {
			if t.ID == teamID {
				inTeam = true
				break
			}
		}
		if !inTeam {
			continue
		}
		if a.Availability.Activity == "available" {
			if best == nil || a.RootItemAssignedCount < best.RootItemAssignedCount {
				best = a
			}
		} else {
			// Non-available but active (logged in) — use as fallback
			if bestFallback == nil || a.RootItemAssignedCount < bestFallback.RootItemAssignedCount {
				bestFallback = a
			}
		}
	}
	if best != nil {
		return best
	}
	return bestFallback
}

// LookupConversationByPhone queries Bird's Conversations API to find the active
// conversation for a phone number. Lists active conversations for the channel
// and matches by featuredParticipants contact identifierValue.
// Returns the conversation ID or "" if not found.
func (c *Client) LookupConversationByPhone(phone string) (string, error) {
	if phone == "" {
		return "", nil
	}

	reqURL := fmt.Sprintf("%s/workspaces/%s/conversations?channelId=%s&status=active&limit=50",
		c.conversationsBase(), c.workspaceID, c.channelID)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("create conversation lookup request: %w", err)
	}
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("lookup conversation: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("lookup conversation: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []struct {
			ID                   string `json:"id"`
			FeaturedParticipants []struct {
				Contact struct {
					IdentifierValue string `json:"identifierValue"`
				} `json:"contact"`
			} `json:"featuredParticipants"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse conversation lookup: %w", err)
	}

	// Find conversation where a participant matches the phone
	for _, conv := range result.Results {
		for _, p := range conv.FeaturedParticipants {
			if p.Contact.IdentifierValue == phone {
				slog.Info("conversation_lookup_success",
					"phone", phone,
					"conversation_id", conv.ID,
				)
				c.CacheConversationID(phone, conv.ID)
				return conv.ID, nil
			}
		}
	}

	return "", nil
}

// EscalateToAgent assigns a conversation to the best available agent in Bird Inbox.
// Flow: resolve conversationID (API lookup if needed) → mark conversation →
// list active agents → pick least loaded in teamID →
// fallback to fallbackTeamID → assign to team without agent if nobody available.
func (c *Client) EscalateToAgent(conversationID, phone, teamID, teamName, patientName, fallbackTeamID string) error {
	// If no conversationID or it might be stale, try API lookup by phone
	if phone != "" {
		if conversationID == "" {
			lookedUp, err := c.LookupConversationByPhone(phone)
			if err != nil {
				slog.Warn("conversation_lookup_failed", "phone", phone, "error", err)
			} else if lookedUp != "" {
				conversationID = lookedUp
			}
		} else {
			// Verify the ID is still active via cache (populated by processMessage lookup)
			if cached := c.GetCachedConversationID(phone); cached != "" && cached != conversationID {
				slog.Info("escalation_conversation_id_refreshed",
					"phone", phone,
					"old", conversationID,
					"new", cached,
				)
				conversationID = cached
			}
		}
	}

	if conversationID == "" {
		return fmt.Errorf("empty conversation ID")
	}

	slog.Debug("escalate_to_agent_start",
		"conversation_id", conversationID,
		"team_id", teamID,
		"team_name", teamName,
	)

	// 1. Mark conversation name (visible in Bird Inbox, non-blocking)
	if err := c.MarkConversationEscalated(conversationID, teamName, patientName); err != nil {
		slog.Warn("mark escalated failed (non-blocking)", "error", err)
	}

	// 2. List active agents; if none active, fall back to all agents (pick least loaded)
	agents, err := c.ListActiveAgents()
	if err != nil {
		slog.Warn("list active agents failed, falling back to all agents",
			"conversation_id", conversationID,
			"team_id", teamID,
			"error", err,
		)
		agents, err = c.ListAllAgents()
		if err != nil {
			slog.Warn("list all agents failed, assigning to team only",
				"conversation_id", conversationID,
				"team_id", teamID,
				"error", err,
			)
			return c.AssignFeedItem(conversationID, teamID, "")
		}
	}
	if len(agents) == 0 {
		slog.Warn("no agents found at all, assigning to team only",
			"conversation_id", conversationID,
			"team_id", teamID,
		)
		return c.AssignFeedItem(conversationID, teamID, "")
	}

	// 3. Pick least loaded agent in target team
	agent := pickLeastLoadedAgent(agents, teamID)
	if agent != nil {
		slog.Info("agent assigned",
			"conversation_id", conversationID,
			"agent_id", agent.ID,
			"agent_name", agent.DisplayName,
			"team_id", teamID,
			"workload", agent.RootItemAssignedCount,
		)
		return c.AssignFeedItem(conversationID, teamID, agent.ID)
	}

	// 4. Fallback: try fallback team (Call Center)
	if fallbackTeamID != "" && fallbackTeamID != teamID {
		agent = pickLeastLoadedAgent(agents, fallbackTeamID)
		if agent != nil {
			slog.Info("agent assigned (fallback team)",
				"conversation_id", conversationID,
				"agent_id", agent.ID,
				"agent_name", agent.DisplayName,
				"team_id", fallbackTeamID,
				"original_team_id", teamID,
				"workload", agent.RootItemAssignedCount,
			)
			return c.AssignFeedItem(conversationID, fallbackTeamID, agent.ID)
		}
	}

	// 5. Agents online but none available (all busy/away) — assign to team, they'll pick up when free
	slog.Warn("no available agents, assigning to team only",
		"conversation_id", conversationID,
		"team_id", teamID,
	)
	return c.AssignFeedItem(conversationID, teamID, "")
}

// UpdateFeedItem updates a conversation's feed item in Bird Inbox.
// closed=true closes the item; teamID/agentID assign it to an agent.
func (c *Client) UpdateFeedItem(conversationID, messageID string, closed bool, teamID, agentID string) error {
	if conversationID == "" {
		return nil // No conversation to update
	}

	url := fmt.Sprintf("%s/workspaces/%s/conversations/%s/feed-items/%s",
		c.conversationsBase(), c.workspaceID, conversationID, messageID)

	payload := map[string]interface{}{
		"closed": closed,
	}
	if teamID != "" {
		payload["assignedTeamId"] = teamID
	}
	if agentID != "" {
		payload["assignedAgentId"] = agentID
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal feed item update: %w", err)
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create feed item request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.apiKeyWA)

	resp, err := c.sendWithRetry(req, 1)
	if err != nil {
		return fmt.Errorf("update feed item: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("feed item update failed", "status", resp.StatusCode, "body", string(respBody))
	}

	return nil
}

// TagConversation adds a tag/label to a conversation in Bird for categorization.
func (c *Client) TagConversation(conversationID, tag string) error {
	if conversationID == "" || tag == "" {
		return nil
	}

	url := fmt.Sprintf("%s/workspaces/%s/conversations/%s/tags",
		c.conversationsBase(), c.workspaceID, conversationID)

	payload := map[string]interface{}{
		"tag": tag,
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal tag: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create tag request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.apiKeyWA)

	resp, err := c.sendWithRetry(req, 1)
	if err != nil {
		slog.Warn("tag conversation failed", "conversation_id", conversationID, "tag", tag, "error", err)
		return nil // Non-critical, don't block flow
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("tag conversation failed", "status", resp.StatusCode, "body", string(respBody))
	}

	return nil
}

// sendMessage envía un payload JSON a la API de Bird con retry.
func (c *Client) sendMessage(url string, payload interface{}) (string, error) {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.apiKeyWA)

	resp, err := c.sendWithRetry(req, 2)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("bird api error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	slog.Debug("bird_api_response", "status", resp.StatusCode, "body", string(respBody))

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		slog.Debug("could not parse bird response", "body", string(respBody))
	}

	return result.ID, nil
}

// sendWithRetry reintenta en 5xx con backoff cuadrático (1s, 4s, 9s). No retry en 4xx.
// Sleeps are context-aware for clean shutdown.
func (c *Client) sendWithRetry(req *http.Request, maxRetries int) (*http.Response, error) {
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
	}

	ctx := req.Context()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt == maxRetries {
				return nil, fmt.Errorf("bird api after %d attempts: %w", maxRetries+1, err)
			}
			delay := time.Duration((attempt+1)*(attempt+1)) * time.Second
			slog.Warn("bird api retry (network)", "attempt", attempt+1, "delay", delay, "error", err)
			if err := sleepWithContext(ctx, delay); err != nil {
				return nil, fmt.Errorf("bird api retry cancelled: %w", err)
			}
			continue
		}

		if resp.StatusCode < 500 {
			return resp, nil
		}

		resp.Body.Close()
		if attempt == maxRetries {
			return nil, fmt.Errorf("bird api 5xx after %d attempts: status %d", maxRetries+1, resp.StatusCode)
		}

		delay := time.Duration((attempt+1)*(attempt+1)) * time.Second
		slog.Warn("bird api retry", "attempt", attempt+1, "status", resp.StatusCode, "delay", delay)
		if err := sleepWithContext(ctx, delay); err != nil {
			return nil, fmt.Errorf("bird api retry cancelled: %w", err)
		}
	}
	return nil, fmt.Errorf("unreachable")
}

// sleepWithContext sleeps for the given duration or returns early if context is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
