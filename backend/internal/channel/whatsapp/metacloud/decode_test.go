package metacloud

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"chatisp/internal/channel/whatsapp"
)

func TestParseTwoNumbersDoesNotChooseTenant(t *testing.T) {
	raw, err := MarshalTextEnvelope(
		TextFixture{
			WABAID: "waba-a", PhoneNumberID: "111", DisplayPhone: "15551110000",
			MessageID: "wamid.a", From: "16505551001", ProfileName: "Ana",
			Body: "olá A", Timestamp: "1749416383",
		},
		TextFixture{
			WABAID: "waba-b", PhoneNumberID: "222", DisplayPhone: "15552220000",
			MessageID: "wamid.b", From: "16505551002", ProfileName: "Bia",
			Body: "olá B", Timestamp: "1749416400",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["tenant_id"] = "tenant-padrao"
	doc["app_id"] = "app-do-payload"
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	ingested := time.Unix(1_800_000_000, 0).UTC()
	items, err := Parse("app-trusted", raw, ingested)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("itens: %#v", items)
	}
	if items[0].PhoneNumberID == items[1].PhoneNumberID || items[0].WABAID == items[1].WABAID {
		t.Fatalf("números: %#v %#v", items[0], items[1])
	}
	seen := map[string]Item{}
	for _, item := range items {
		if item.Kind != ItemMessage || item.AppID != "app-trusted" || item.DedupKey != "message:"+item.ExternalID {
			t.Fatalf("item: %#v", item)
		}
		if item.IngestedAt != ingested || !item.OriginKnown || item.OriginAt.Equal(ingested) {
			t.Fatalf("relógio: %#v", item)
		}
		blob := string(item.Preserved) + item.Text + item.ProfileName + item.WABAID
		if strings.Contains(blob, "tenant-padrao") || strings.Contains(blob, "app-do-payload") {
			t.Fatalf("envelope vazou: %s", blob)
		}
		seen[item.PhoneNumberID] = item
	}
	if seen["111"].Text != "olá A" || seen["222"].Text != "olá B" {
		t.Fatalf("textos: %#v", seen)
	}
	if strings.Contains(string(seen["111"].Preserved), "wamid.b") || strings.Contains(string(seen["222"].Preserved), "wamid.a") {
		t.Fatal("payload preservado inclui o outro número")
	}
	if seen["111"].ProfileName != "Ana" || seen["222"].ProfileName != "Bia" {
		t.Fatalf("nomes: %#v", seen)
	}
}

func TestParseDuplicateStatusKeepsContentInKey(t *testing.T) {
	billable := true
	base := StatusFixture{
		WABAID: "waba-a", PhoneNumberID: "111", DisplayPhone: "15551110000",
		MessageID: "wamid.same", Status: "delivered", Timestamp: "1750263773",
		RecipientID: "16505551234", CallbackData: "callback-opaco",
		PricingCategory: "service", PricingType: "free_customer_service", PricingModel: "PMP",
		Billable: &billable,
	}
	again := base
	read := base
	read.Status = "read"
	read.Timestamp = "1750263800"
	raw, err := MarshalStatusEnvelope(base, again, read)
	if err != nil {
		t.Fatal(err)
	}
	items, err := Parse("app-trusted", raw, time.Unix(1_800_000_100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("itens: %#v", items)
	}
	if items[0].Kind != ItemStatus || items[0].DedupKey == "" || items[0].DedupKey != items[1].DedupKey {
		t.Fatalf("duplicata: %#v %#v", items[0], items[1])
	}
	if items[2].DedupKey == items[0].DedupKey || items[2].Remote != whatsapp.EvidenceRead || items[0].Remote != whatsapp.EvidenceDelivered {
		t.Fatalf("leitura: %#v", items[2])
	}
	if items[0].CallbackData != "callback-opaco" || items[0].PricingCategory != "service" || items[0].Billable == nil || !*items[0].Billable {
		t.Fatalf("conteúdo: %#v", items[0])
	}
	for _, item := range items {
		if strings.Contains(item.DedupKey, "waba-a") && strings.Contains(item.DedupKey, "tenant") {
			t.Fatal(item.DedupKey)
		}
		if item.AppID != "app-trusted" || item.PhoneNumberID != "111" {
			t.Fatalf("roteamento: %#v", item)
		}
	}
	sameID, err := MarshalTextEnvelope(
		TextFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.same", From: "1", Body: "um", Timestamp: "10"},
		TextFixture{WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.same", From: "1", Body: "dois", Timestamp: "11"},
	)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := Parse("app-trusted", sameID, time.Unix(10, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].DedupKey != messages[1].DedupKey || messages[0].Text == messages[1].Text {
		t.Fatalf("dedup de mensagem: %#v", messages)
	}
}

func TestParseQuarantinesUnknownAndIdentityLessItems(t *testing.T) {
	raw := []byte(`{
		"object":"whatsapp_business_account",
		"tenant_id":"tenant-padrao",
		"entry":[{"id":"waba-a","changes":[
			{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"111"},
				"messages":[
					{"from":"1650","timestamp":"10","type":"order","order":{"id":"media-1"}},
					{"from":"1650","timestamp":"11","type":"text","text":{"body":"sem id"}}
				],
				"statuses":[{"status":"mystery","timestamp":"12","recipient_id":"1650"}]
			}},
			{"field":"account_update","value":{"phone_number":"1555"}}
		]}]
	}`)
	items, err := Parse("app-trusted", raw, time.Unix(50, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]int{}
	for _, item := range items {
		if item.Kind != ItemQuarantine || item.Reason == "" || item.AppID != "app-trusted" {
			t.Fatalf("quarentena: %#v", item)
		}
		if strings.Contains(string(item.Preserved), "tenant-padrao") || strings.Contains(item.DedupKey, "tenant-padrao") {
			t.Fatalf("envelope na quarentena: %#v", item)
		}
		reasons[item.Reason]++
	}
	if reasons[ReasonUnsupportedType] != 1 || reasons[ReasonMissingIdentity] != 1 || reasons[ReasonUnknownStatus] != 1 || reasons[ReasonUnsupportedField] != 1 {
		t.Fatalf("razões: %#v itens=%#v", reasons, items)
	}
	for _, item := range items {
		if len(item.Preserved) == 0 {
			t.Fatalf("quarentena sem objeto: %#v", item)
		}
	}
	if _, err := Parse(" ", raw, time.Time{}); err == nil {
		t.Fatal("app vazio")
	}
	bad, err := Parse("app-trusted", []byte("{"), time.Unix(1, 0).UTC())
	if err != nil || len(bad) != 1 || bad[0].Reason != ReasonInvalidJSON || string(bad[0].Preserved) != "{" {
		t.Fatalf("json: %v %#v", err, bad)
	}
}

func TestParseOverItemLimitKeepsRemainder(t *testing.T) {
	const extra = 3
	fixtures := make([]TextFixture, maxItems+extra)
	for i := range fixtures {
		fixtures[i] = TextFixture{
			WABAID: "waba-a", PhoneNumberID: "111", MessageID: "wamid.bulk." + strconv.Itoa(i),
			From: "15550001111", Body: "m", Timestamp: "10",
		}
	}
	raw, err := MarshalTextEnvelope(fixtures...)
	if err != nil {
		t.Fatal(err)
	}
	other, err := MarshalTextEnvelope(TextFixture{
		WABAID: "waba-b", PhoneNumberID: "222", MessageID: "wamid.tail",
		From: "15550002222", Body: "fim", Timestamp: "11",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw = joinEntries(t, raw, other)
	items, err := Parse("app-trusted", raw, time.Unix(20, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	messages := 0
	tooMany := 0
	var tail string
	for _, item := range items {
		switch {
		case item.Kind == ItemMessage:
			messages++
			if item.WABAID == "" || item.PhoneNumberID == "" {
				t.Fatalf("mensagem sem identidade: %#v", item)
			}
		case item.Reason == ReasonTooMany:
			tooMany++
			if len(item.Preserved) == 0 {
				t.Fatal("excesso sem payload")
			}
			tail += string(item.Preserved)
		default:
			t.Fatalf("item inesperado: %#v", item)
		}
	}
	if messages != maxItems || tooMany != extra+1 {
		t.Fatalf("messages=%d tooMany=%d items=%d", messages, tooMany, len(items))
	}
	if !strings.Contains(tail, "wamid.bulk."+strconv.Itoa(maxItems)) || !strings.Contains(tail, "wamid.tail") {
		t.Fatalf("excesso perdido: %s", tail)
	}
	if strings.Contains(tail, "wamid.bulk.0") {
		t.Fatal("primeiro item foi para a quarentena")
	}
}

func TestParseEmptyWABAAndFromAreQuarantine(t *testing.T) {
	raw := []byte(`{
		"object":"whatsapp_business_account",
		"entry":[
			{"id":"","changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"111"},
				"messages":[{"from":"1555","id":"wamid.sem-waba","timestamp":"10","type":"text","text":{"body":"oi"}}],
				"statuses":[{"id":"wamid.st","status":"delivered","timestamp":"10","recipient_id":"1555"}]}}]},
			{"id":"waba-a","changes":[{"field":"messages","value":{"metadata":{"phone_number_id":"111"},
				"messages":[{"from":"","id":"wamid.sem-from","timestamp":"10","type":"text","text":{"body":"sem origem"}}],
				"statuses":[{"status":"read","timestamp":"12","recipient_id":"1555"}]}}]},
			{"id":"waba-a","changes":[{"field":"messages","value":{"metadata":{}}}]}
		]
	}`)
	items, err := Parse("app-trusted", raw, time.Unix(30, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("itens: %#v", items)
	}
	for _, item := range items {
		if item.Kind != ItemQuarantine || len(item.Preserved) == 0 {
			t.Fatalf("item: %#v", item)
		}
	}
	blob := ""
	reasons := map[string]int{}
	for _, item := range items {
		reasons[item.Reason]++
		blob += string(item.Preserved)
		if item.ExternalID == "wamid.sem-waba" || item.ExternalID == "wamid.st" || item.ExternalID == "wamid.sem-from" {
			if item.Reason != ReasonMissingIdentity {
				t.Fatalf("razão: %#v", item)
			}
		}
	}
	if reasons[ReasonMissingIdentity] != 4 || reasons[ReasonMissingPhone] != 1 {
		t.Fatalf("razões: %#v", reasons)
	}
	for _, needle := range []string{"wamid.sem-waba", "wamid.sem-from", "wamid.st", "read"} {
		if !strings.Contains(blob, needle) {
			t.Fatalf("payload %s ausente", needle)
		}
	}
}

func joinEntries(t *testing.T, first, second []byte) []byte {
	t.Helper()
	var a, b map[string]any
	if err := json.Unmarshal(first, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second, &b); err != nil {
		t.Fatal(err)
	}
	entries, _ := a["entry"].([]any)
	more, _ := b["entry"].([]any)
	a["entry"] = append(entries, more...)
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
