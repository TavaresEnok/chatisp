package httpapi

import (
	"strings"
	"testing"
)

func TestM4ModulesAreRegistered(t *testing.T) {
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	for _, fragment := range []string{
		"/api/panel/canned-replies",
		"/api/panel/inboxes/{inbox_id}/channel-policy/contacts/{contact_id}/consents",
		"/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/attachments",
		"/api/panel/inboxes/{inbox_id}/business-hours",
		"/template-messages",
	} {
		if !strings.Contains(document, fragment) {
			t.Fatalf("contrato ausente: %s", fragment)
		}
	}
}
