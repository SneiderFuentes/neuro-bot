package bird

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// computeBirdSignature replica el algoritmo exacto de Bird (ejemplo oficial Go):
// 1. bh = SHA256(body) → [32]byte raw
// 2. payload = fmt.Sprintf("%s\n%s\n%s", timestamp, url, bh)  ← %s = raw bytes
// 3. sig = base64(HMAC-SHA256(key, payload))
func computeBirdSignature(secret, timestamp, url string, body []byte) string {
	bh := sha256.Sum256(body)
	var m bytes.Buffer
	fmt.Fprintf(&m, "%s\n%s\n%s", timestamp, url, bh)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(m.Bytes())
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature_Valid(t *testing.T) {
	c := &Client{WebhookSecret: "my-secret"}
	body := []byte(`{"event":"message.created"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	url := "https://example.com/api/webhooks/whatsapp"

	sig := computeBirdSignature("my-secret", timestamp, url, body)

	if !c.VerifyWebhookSignature(sig, timestamp, url, body) {
		t.Error("expected valid signature")
	}
}

func TestVerifyWebhookSignature_Invalid(t *testing.T) {
	c := &Client{WebhookSecret: "my-secret"}
	body := []byte(`{"event":"message.created"}`)
	if c.VerifyWebhookSignature("aW52YWxpZC1zaWduYXR1cmU=", "1700000000", "https://example.com/webhook", body) {
		t.Error("expected invalid signature to be rejected")
	}
}

func TestVerifyWebhookSignature_Empty(t *testing.T) {
	c := &Client{WebhookSecret: "my-secret"}
	body := []byte(`test`)

	if c.VerifyWebhookSignature("", "1700000000", "https://example.com/webhook", body) {
		t.Error("empty signature should return false")
	}
	if c.VerifyWebhookSignature("something", "", "https://example.com/webhook", body) {
		t.Error("empty timestamp should return false")
	}
}

func TestVerifyWebhookSignature_InvalidBase64(t *testing.T) {
	c := &Client{WebhookSecret: "my-secret"}
	body := []byte(`test`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	if c.VerifyWebhookSignature("not-valid-base64!!!", timestamp, "https://example.com/webhook", body) {
		t.Error("invalid base64 should return false")
	}
}

func TestVerifyWebhookSignature_ExpiredTimestamp(t *testing.T) {
	c := &Client{WebhookSecret: "my-secret"}
	body := []byte(`test`)
	url := "https://example.com/webhook"
	oldTimestamp := strconv.FormatInt(time.Now().Add(-20*time.Minute).Unix(), 10)
	sig := computeBirdSignature("my-secret", oldTimestamp, url, body)

	if c.VerifyWebhookSignature(sig, oldTimestamp, url, body) {
		t.Error("expired timestamp should return false")
	}
}

func TestVerifyWebhookSignature_DifferentURL(t *testing.T) {
	c := &Client{WebhookSecret: "my-secret"}
	body := []byte(`{"event":"message.created"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	sig := computeBirdSignature("my-secret", timestamp, "https://example.com/api/webhooks/whatsapp", body)

	if c.VerifyWebhookSignature(sig, timestamp, "https://example.com/api/webhooks/other", body) {
		t.Error("different URL should make signature invalid")
	}
}

func TestParseInboundMessage_Text(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID: "msg-1",
			Sender: SenderInfo{
				DisplayName: "Juan",
				Contacts:    []Contact{{IdentifierValue: "+573001234567"}},
			},
			Body:           MessageBody{Type: "text", Text: TextBody{Text: "Hola"}},
			ConversationID: "conv-1",
		},
	}

	msg := ParseInboundMessage(event)
	if msg.ID != "msg-1" {
		t.Errorf("expected msg-1, got %s", msg.ID)
	}
	if msg.Phone != "+573001234567" {
		t.Errorf("expected phone, got %s", msg.Phone)
	}
	if msg.MessageType != "text" {
		t.Errorf("expected text, got %s", msg.MessageType)
	}
	if msg.Text != "Hola" {
		t.Errorf("expected Hola, got %s", msg.Text)
	}
	if msg.ConversationID != "conv-1" {
		t.Errorf("expected conv-1, got %s", msg.ConversationID)
	}
	if msg.IsPostback {
		t.Error("should not be postback")
	}
}

func TestParseInboundMessage_Postback(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID: "msg-pb",
			Sender: SenderInfo{
				Contacts: []Contact{{IdentifierValue: "+573001234567"}},
			},
			Body: MessageBody{
				Type: "text",
				Text: TextBody{
					Text: "button text",
					Actions: []Action{
						{Type: "postback", Postback: Postback{Text: "Confirmar", Payload: "confirm"}},
					},
				},
			},
		},
	}

	msg := ParseInboundMessage(event)
	if msg.MessageType != "postback" {
		t.Errorf("expected postback, got %s", msg.MessageType)
	}
	if !msg.IsPostback {
		t.Error("expected IsPostback=true")
	}
	if msg.PostbackPayload != "confirm" {
		t.Errorf("expected confirm, got %s", msg.PostbackPayload)
	}
}

func TestParseInboundMessage_Image(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID: "msg-img",
			Sender: SenderInfo{
				Contacts: []Contact{{IdentifierValue: "+573001234567"}},
			},
			Body: MessageBody{
				Type: "image",
				Text: TextBody{Text: "https://example.com/image.jpg"},
			},
		},
	}

	msg := ParseInboundMessage(event)
	if msg.MessageType != "image" {
		t.Errorf("expected image, got %s", msg.MessageType)
	}
	if msg.ImageURL != "https://example.com/image.jpg" {
		t.Errorf("expected image URL, got %s", msg.ImageURL)
	}
}

func TestParseInboundMessage_Audio(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID:     "msg-aud",
			Sender: SenderInfo{Contacts: []Contact{{IdentifierValue: "+573001234567"}}},
			Body:   MessageBody{Type: "audio"},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.MessageType != "audio" {
		t.Errorf("expected audio, got %s", msg.MessageType)
	}
}

func TestParseInboundMessage_NoSenderContacts(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID:   "msg-nocontact",
			Body: MessageBody{Type: "text", Text: TextBody{Text: "hi"}},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.Phone != "" {
		t.Errorf("expected empty phone, got %s", msg.Phone)
	}
}

func TestExtractPostbackPayload_NoActions(t *testing.T) {
	body := MessageBody{Type: "text", Text: TextBody{Text: "hello"}}
	payload, ok := ExtractPostbackPayload(body)
	if ok {
		t.Error("expected false for no actions")
	}
	if payload != "" {
		t.Errorf("expected empty payload, got %s", payload)
	}
}

func TestExtractPostbackPayload_NonPostbackAction(t *testing.T) {
	body := MessageBody{
		Type: "text",
		Text: TextBody{
			Actions: []Action{{Type: "reply", Postback: Postback{Payload: "data"}}},
		},
	}
	_, ok := ExtractPostbackPayload(body)
	if ok {
		t.Error("expected false for non-postback action type")
	}
}

func TestParseInboundMessage_Video(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID:     "msg-vid",
			Sender: SenderInfo{Contacts: []Contact{{IdentifierValue: "+573001234567"}}},
			Body:   MessageBody{Type: "video"},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.MessageType != "video" {
		t.Errorf("expected video, got %s", msg.MessageType)
	}
}

func TestParseInboundMessage_Location(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID:     "msg-loc",
			Sender: SenderInfo{Contacts: []Contact{{IdentifierValue: "+573001234567"}}},
			Body:   MessageBody{Type: "location"},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.MessageType != "location" {
		t.Errorf("expected location, got %s", msg.MessageType)
	}
}

func TestParseInboundMessage_Contact(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID:     "msg-contact",
			Sender: SenderInfo{Contacts: []Contact{{IdentifierValue: "+573001234567"}}},
			Body:   MessageBody{Type: "contacts"},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.MessageType != "contact" {
		t.Errorf("expected contact, got %s", msg.MessageType)
	}
}

func TestParseInboundMessage_Sticker(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID:     "msg-sticker",
			Sender: SenderInfo{Contacts: []Contact{{IdentifierValue: "+573001234567"}}},
			Body:   MessageBody{Type: "sticker"},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.MessageType != "sticker" {
		t.Errorf("expected sticker, got %s", msg.MessageType)
	}
}

func TestParseInboundMessage_Document(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID:     "msg-doc",
			Sender: SenderInfo{Contacts: []Contact{{IdentifierValue: "+573001234567"}}},
			Body:   MessageBody{Type: "document"},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.MessageType != "document" {
		t.Errorf("expected document, got %s", msg.MessageType)
	}
}

func TestParseInboundMessage_UnknownType(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID:     "msg-unknown",
			Sender: SenderInfo{Contacts: []Contact{{IdentifierValue: "+573001234567"}}},
			Body:   MessageBody{Type: "reactions"},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.MessageType != "reactions" {
		t.Errorf("expected reactions (passthrough), got %s", msg.MessageType)
	}
}

func TestParseInboundMessage_DisplayName(t *testing.T) {
	event := WebhookEvent{
		Payload: WebhookPayload{
			ID: "msg-dn",
			Sender: SenderInfo{
				DisplayName: "Carlos García",
				Contacts:    []Contact{{IdentifierValue: "+573001234567"}},
			},
			Body: MessageBody{Type: "text", Text: TextBody{Text: "hi"}},
		},
	}
	msg := ParseInboundMessage(event)
	if msg.DisplayName != "Carlos García" {
		t.Errorf("expected display name, got %s", msg.DisplayName)
	}
}
