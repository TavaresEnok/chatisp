package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
)

// textPayload is the canonical send body. Field order is the hash order.
type textPayload struct {
	V              int    `json:"v"`
	ConversationID string `json:"conversation_id"`
	ConnectorID    string `json:"connector_id"`
	Body           string `json:"body"`
	Visibility     string `json:"visibility"`
}

func canonicalSend(conversationID, connectorID, body string) ([]byte, error) {
	return json.Marshal(textPayload{
		V:              payloadVersion,
		ConversationID: conversationID,
		ConnectorID:    connectorID,
		Body:           body,
		Visibility:     "public",
	})
}

// templatePayload is the canonical proactive template. Language and purpose
// belong to the idempotency hash; the visible body stays the template id.
type templatePayload struct {
	V              int    `json:"v"`
	ConversationID string `json:"conversation_id"`
	ConnectorID    string `json:"connector_id"`
	Body           string `json:"body"`
	Visibility     string `json:"visibility"`
	Kind           string `json:"kind"`
	Mode           string `json:"mode"`
	Purpose        string `json:"purpose"`
	Language       string `json:"language"`
	TemplateID     string `json:"template_id"`
}

type mediaPayload struct {
	V              int    `json:"v"`
	ConversationID string `json:"conversation_id"`
	ConnectorID    string `json:"connector_id"`
	Body           string `json:"body"`
	Visibility     string `json:"visibility"`
	Kind           string `json:"kind"`
	AttachmentID   string `json:"attachment_id"`
	ReplyTo        string `json:"reply_to"`
}

func canonicalMedia(conversationID, connectorID, body, kind, attachmentID, replyTo string) ([]byte, error) {
	return json.Marshal(mediaPayload{
		V:              payloadVersion,
		ConversationID: conversationID,
		ConnectorID:    connectorID,
		Body:           body,
		Visibility:     "public",
		Kind:           kind,
		AttachmentID:   attachmentID,
		ReplyTo:        replyTo,
	})
}

func canonicalTemplate(conversationID, connectorID, body, mode, purpose, language, templateID string) ([]byte, error) {
	return json.Marshal(templatePayload{
		V:              payloadVersion,
		ConversationID: conversationID,
		ConnectorID:    connectorID,
		Body:           body,
		Visibility:     "public",
		Kind:           "template",
		Mode:           mode,
		Purpose:        strings.TrimSpace(purpose),
		Language:       strings.TrimSpace(language),
		TemplateID:     strings.TrimSpace(templateID),
	})
}

func commandHash(parts ...string) []byte {
	mac := sha256.New()
	var n [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		_, _ = mac.Write(n[:])
		_, _ = mac.Write([]byte(part))
	}
	return mac.Sum(nil)
}

func closeHash(in CloseInput) []byte {
	return commandHash(
		in.ConversationID.String(),
		strconv.FormatInt(in.ExpectedVersion, 10),
		strings.TrimSpace(in.Reason),
		strings.TrimSpace(in.Result),
		strings.TrimSpace(in.Summary),
		strings.TrimSpace(in.FarewellBody),
		strings.TrimSpace(in.FarewellRequestID),
	)
}

func reopenHash(in ReopenInput) []byte {
	return commandHash(
		in.ConversationID.String(),
		strconv.FormatInt(in.ExpectedVersion, 10),
	)
}
