package metacloud

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseInboundTypesContextAndQuarantine(t *testing.T) {
	lat, lng := -23.55, -46.63
	raw, err := MarshalMessageEnvelope(
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.text", From: "5511999990001", Timestamp: "10", Type: "text", ContextID: "wamid.quoted", Object: map[string]string{"body": "oi"}, ProfileName: "Ana"},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.image", From: "5511999990001", Timestamp: "11", Type: "image", ContextID: "wamid.text", Object: map[string]any{"id": "100", "mime_type": "image/jpeg", "caption": "foto"}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.doc", From: "5511999990001", Timestamp: "12", Type: "document", Object: map[string]any{"id": "101", "mime_type": "application/pdf", "filename": "boleto.pdf", "caption": "doc"}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.audio", From: "5511999990001", Timestamp: "13", Type: "audio", Object: map[string]any{"id": "102", "mime_type": "audio/ogg; codecs=opus", "voice": true}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.video", From: "5511999990001", Timestamp: "14", Type: "video", Object: map[string]any{"id": "103", "mime_type": "video/mp4", "caption": "clipe"}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.sticker", From: "5511999990001", Timestamp: "15", Type: "sticker", Object: map[string]any{"id": "104", "mime_type": "image/webp"}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.loc", From: "5511999990001", Timestamp: "16", Type: "location", Object: map[string]any{"latitude": lat, "longitude": lng, "name": "Sede", "address": "Rua 1"}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.card", From: "5511999990001", Timestamp: "17", Type: "contacts", Object: []any{map[string]any{"name": map[string]string{"formatted_name": "Bia"}, "phones": []any{map[string]string{"phone": "+5511988887777"}}, "org": map[string]string{"company": "ISP"}}}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.react", From: "5511999990001", Timestamp: "18", Type: "reaction", Object: map[string]string{"message_id": "wamid.text", "emoji": "👍"}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.btn", From: "5511999990001", Timestamp: "19", Type: "interactive", Object: map[string]any{"type": "button_reply", "button_reply": map[string]string{"id": "yes", "title": "Sim"}}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.list", From: "5511999990001", Timestamp: "20", Type: "interactive", Object: map[string]any{"type": "list_reply", "list_reply": map[string]string{"id": "plan", "title": "Plano"}}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.legacy", From: "5511999990001", Timestamp: "21", Type: "button", Object: map[string]string{"payload": "ok", "text": "Ok"}},
		MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.unknown", From: "5511999990001", Timestamp: "22", Type: "system", Object: map[string]string{"body": "x"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	items, err := Parse("app-trusted", raw, time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Item{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	text := byID["wamid.text"]
	if text.Kind != ItemMessage || text.Text != "oi" || text.Content.Type != "text" || text.Content.ContextID != "wamid.quoted" {
		t.Fatalf("texto: %#v", text)
	}
	image := byID["wamid.image"]
	if image.Kind != ItemMessage || image.Text != "foto" || image.Content.Type != "image" || image.Content.MediaID != "100" || image.Content.MIME != "image/jpeg" || image.Content.Caption != "foto" || image.Content.ContextID != "wamid.text" {
		t.Fatalf("imagem: %#v", image.Content)
	}
	doc := byID["wamid.doc"]
	if doc.Content.Type != "document" || doc.Content.Filename != "boleto.pdf" || doc.Content.MediaID != "101" {
		t.Fatalf("documento: %#v", doc.Content)
	}
	audio := byID["wamid.audio"]
	if audio.Content.Type != "voice" || !audio.Content.Voice || audio.Content.MediaID != "102" {
		t.Fatalf("áudio: %#v", audio.Content)
	}
	if byID["wamid.video"].Content.Type != "video" || byID["wamid.video"].Content.Caption != "clipe" {
		t.Fatalf("vídeo: %#v", byID["wamid.video"].Content)
	}
	if byID["wamid.sticker"].Content.Type != "sticker" || byID["wamid.sticker"].Content.MIME != "image/webp" {
		t.Fatalf("figurinha: %#v", byID["wamid.sticker"].Content)
	}
	loc := byID["wamid.loc"]
	if loc.Content.Type != "location" || loc.Content.Latitude == nil || *loc.Content.Latitude != lat || loc.Content.Address != "Rua 1" || loc.Content.LocationName != "Sede" {
		t.Fatalf("local: %#v", loc.Content)
	}
	card := byID["wamid.card"]
	if card.Content.Type != "contacts" || len(card.Content.Contacts) != 1 || card.Content.Contacts[0].Name != "Bia" || !strings.Contains(card.Content.Contacts[0].VCard, "BEGIN:VCARD") || !strings.Contains(card.Content.Contacts[0].VCard, "+5511988887777") {
		t.Fatalf("vcard: %#v", card.Content)
	}
	react := byID["wamid.react"]
	if react.Kind != ItemMessage || react.Content.Type != "reaction" || react.Content.Emoji != "👍" || react.Content.ReactionTarget != "wamid.text" {
		t.Fatalf("reação: %#v", react.Content)
	}
	if byID["wamid.btn"].Content.InteractiveType != "button_reply" || byID["wamid.btn"].Content.ReplyID != "yes" || byID["wamid.btn"].Content.Title != "Sim" {
		t.Fatalf("botão: %#v", byID["wamid.btn"].Content)
	}
	if byID["wamid.list"].Content.InteractiveType != "list_reply" || byID["wamid.list"].Content.ReplyID != "plan" {
		t.Fatalf("lista: %#v", byID["wamid.list"].Content)
	}
	if byID["wamid.legacy"].Content.Type != "button" || byID["wamid.legacy"].Content.ReplyID != "ok" {
		t.Fatalf("button legado: %#v", byID["wamid.legacy"].Content)
	}
	unknown := byID["wamid.unknown"]
	if unknown.Kind != ItemQuarantine || unknown.Reason != ReasonUnsupportedType {
		t.Fatalf("desconhecido: %#v", unknown)
	}

	bad, err := Parse("app-trusted", []byte(`{"object":"whatsapp_business_account","entry":[{"id":"waba-a","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"111"},"messages":[{"from":"5511999990001","id":"wamid.bad","timestamp":"30","type":"image","image":{"caption":"sem id"}}]}}]}]}`), time.Unix(1, 0).UTC())
	if err != nil || len(bad) != 1 || bad[0].Kind != ItemQuarantine || bad[0].Reason != ReasonInvalidJSON || bad[0].ExternalID != "wamid.bad" {
		t.Fatalf("malformado: %v %#v", err, bad)
	}
	broken, err := Parse("app-trusted", []byte(`{"object":"whatsapp_business_account","entry":[{"id":"waba-a","changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"111"},"messages":[{"from":"1","id":"wamid.bad2","timestamp":"31","type":"location","location":"norte"}]}}]}]}`), time.Unix(1, 0).UTC())
	if err != nil || len(broken) != 1 || broken[0].Reason != ReasonInvalidJSON {
		t.Fatalf("local malformado: %v %#v", err, broken)
	}
	marker, _ := json.Marshal(map[string]string{"type": "reaction", "reaction_target": "wamid.alvo", "emoji": "👍"})
	injected := "\x1echatisp-in1:" + base64.RawURLEncoding.EncodeToString(marker)
	attack, err := MarshalMessageEnvelope(MessageFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.inject", From: "5511999990001", Timestamp: "40", Type: "text", Object: map[string]string{"body": injected}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse("app-trusted", attack, time.Unix(1, 0).UTC())
	if err != nil || len(parsed) != 1 || parsed[0].Text != injected || parsed[0].Content.Type != "text" || parsed[0].Content.ReactionTarget != "" {
		t.Fatalf("marcador no texto: %v %#v", err, parsed)
	}
}
