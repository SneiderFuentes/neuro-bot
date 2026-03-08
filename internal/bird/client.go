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
	req.Header.Set("Authorization", "Bearer "+c.apiKeyWA)

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

	// Self-heal: if the conversation is closed, look up the current one and retry
	if errors.Is(err, ErrConversationNotActive) {
		if freshID, lookErr := c.LookupConversationByPhone(phone); lookErr == nil && freshID != "" && freshID != conversationID {
			slog.Info("conversation_id_self_healed",
				"phone", phone,
				"old", conversationID,
				"new", freshID,
			)
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

// SendList envía un mensaje con lista interactiva.
// Routes via Conversations API when conversationID is available.
func (c *Client) SendList(to, conversationID, body, buttonLabel string, sections []ListSection) (string, error) {
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
	if id, _, ok := c.trySendToConversation(to, conversationID, msgBody); ok {
		return id, nil
	}
	// Fallback: Channels API
	payload := map[string]interface{}{
		"receiver": map[string]interface{}{
			"contacts": []map[string]string{{"identifierValue": to}},
		},
		"body": msgBody,
	}
	return c.sendMessage(c.messagesURL(), payload)
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
			"type":  p.Type,
			"key":   p.Key,
			"value": p.Value,
		}
	}

	payload := map[string]interface{}{
		"receiver": map[string]interface{}{
			"contacts": []map[string]string{
				{"identifierValue": to},
			},
		},
		"template": map[string]interface{}{
			"projectId":  tmpl.ProjectID,
			"version":    tmpl.VersionID,
			"locale":     tmpl.Locale,
			"parameters": params,
		},
	}
	return c.sendMessage(c.templatesURL(), payload)
}

// PlaceCall inicia una llamada IVR via Bird Voice
func (c *Client) PlaceCall(to string, params map[string]string) (string, error) {
	// Se implementará completamente en Fase 12 (Notificaciones)
	return "", nil
}

// ListActiveAgents returns all agents with status "active" from Bird Inbox.
func (c *Client) ListActiveAgents() ([]AgentInfo, error) {
	url := fmt.Sprintf("%s/workspaces/%s/agents?agentStatuses=active",
		c.conversationsBase(), c.workspaceID)

	req, err := http.NewRequest("GET", url, nil)
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

	req, err := http.NewRequest("PATCH", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create feed item assignment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("assign feed item: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("assign feed item: status %d, body: %s", resp.StatusCode, string(respBody))
	}
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

// CloseConversation sets a conversation's status to "closed" in Bird Inbox.
// PATCH /workspaces/{workspaceId}/conversations/{conversationId} { "status": "closed" }
// This makes the conversation appear as closed in the Inbox UI.
func (c *Client) CloseConversation(conversationID string) error {
	if conversationID == "" {
		return nil
	}

	url := fmt.Sprintf("%s/workspaces/%s/conversations/%s",
		c.conversationsBase(), c.workspaceID, conversationID)

	payload := map[string]interface{}{
		"status": "closed",
	}

	jsonBody, _ := json.Marshal(payload)

	req, err := http.NewRequest("PATCH", url, bytes.NewReader(jsonBody))
	if err != nil {
		slog.Warn("close_conversation_request_failed", "error", err, "conversation_id", conversationID)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "AccessKey "+c.accessKeyID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Warn("close_conversation_failed", "error", err, "conversation_id", conversationID)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		slog.Warn("close_conversation_error",
			"status", resp.StatusCode,
			"body", string(respBody),
			"conversation_id", conversationID)
		return fmt.Errorf("close conversation: status %d", resp.StatusCode)
	}

	slog.Info("conversation_closed", "conversation_id", conversationID)
	return nil
}

// pickLeastLoadedAgent filters agents by teamID and available activity,
// then returns the one with the lowest workload (rootItemAssignedCount).
func pickLeastLoadedAgent(agents []AgentInfo, teamID string) *AgentInfo {
	var best *AgentInfo
	for i := range agents {
		a := &agents[i]
		if a.Availability.Activity != "available" {
			continue
		}
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
		if best == nil || a.RootItemAssignedCount < best.RootItemAssignedCount {
			best = a
		}
	}
	return best
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

	// 2. List active agents — error means nobody can handle the conversation
	agents, err := c.ListActiveAgents()
	if err != nil {
		return fmt.Errorf("no agents available (api error): %w", err)
	}
	if len(agents) == 0 {
		return fmt.Errorf("no active agents online")
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
