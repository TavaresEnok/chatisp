package metacloud

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"chatisp/internal/channel/whatsapp"
)

const (
	// ItemMessage is one inbound text message with a stable external id.
	ItemMessage = "message"
	// ItemStatus is one delivery evidence event, separate from local acceptance.
	ItemStatus = "status"
	// ItemQuarantine is an item that must not enter a tenant inbox.
	ItemQuarantine = "quarantine"

	ReasonInvalidJSON       = "json_invalido"
	ReasonTooLarge          = "corpo_acima_do_limite"
	ReasonUnsupportedField  = "campo_nao_suportado"
	ReasonMissingPhone      = "numero_ausente"
	ReasonMissingIdentity   = "sem_identidade"
	ReasonUnsupportedType   = "tipo_nao_suportado"
	ReasonUnknownStatus     = "status_desconhecido"
	ReasonGroupOutOfScope   = "grupo_fora_de_escopo"
	ReasonTooMany           = "limite_de_itens"
	ReasonItemTooLarge      = "item_acima_do_limite"
	ReasonUnsupportedObject = "objeto_nao_suportado"

	// maxItems is a global cap across every entry and change. Items past it
	// are quarantined with their object; they are not discarded.
	maxItems       = 128
	maxItemBytes   = 64 << 10
	objectWhatsApp = "whatsapp_business_account"
	fieldMessages  = "messages"
)

// Item is one routable fact from an authenticated envelope.
// There is no tenant. AppID is the trusted binding passed by the caller.
// Preserved is only this message or status object, never the whole envelope.
type Item struct {
	Kind          string
	AppID         string
	WABAID        string
	PhoneNumberID string
	DisplayPhone  string

	ExternalID string
	DedupKey   string

	OriginAt    time.Time
	OriginKnown bool
	IngestedAt  time.Time

	From        string
	Text        string
	ProfileName string
	Content     InboundContent

	Status       string
	Remote       whatsapp.RemoteEvidence
	RecipientID  string
	CallbackData string
	ErrorCodes   []int

	PricingCategory string
	PricingType     string
	PricingModel    string
	Billable        *bool

	Preserved []byte
	Reason    string
}

// Parse expands an authenticated envelope into independent items.
// appID is the trusted binding. The payload cannot select a tenant, an app,
// or a default inbox. ingestedAt is the local clock and is not read from JSON.
//
// Every message and status inside the body is counted once, across entries.
// The first maxItems stay as decoded. Anything beyond that becomes its own
// quarantine with ReasonTooMany and the item object preserved. Nothing past
// the cap is dropped, and the cap does not emit a second empty marker.
func Parse(appID string, raw []byte, ingestedAt time.Time) ([]Item, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" || len(appID) > 128 || strings.ContainsAny(appID, "\r\n\x00") {
		return nil, errAppMissing
	}
	if len(raw) > MaxRawBytes {
		return []Item{quarantine(appID, "", "", ReasonTooLarge, clip(raw), ingestedAt, hashKey(ReasonTooLarge, raw[:64], len(raw)))}, nil
	}
	var doc wireDoc
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Object == "" {
		return []Item{quarantine(appID, "", "", ReasonInvalidJSON, clip(raw), ingestedAt, hashKey(ReasonInvalidJSON, raw, len(raw)))}, nil
	}
	if doc.Object != objectWhatsApp {
		return []Item{quarantine(appID, "", "", ReasonUnsupportedObject, clip(raw), ingestedAt, hashKey(ReasonUnsupportedObject, raw, len(raw)))}, nil
	}
	items := make([]Item, 0)
	var overflow []Item
	for _, entry := range doc.Entry {
		for _, change := range entry.Changes {
			for _, item := range expandChange(appID, entry.ID, change, ingestedAt) {
				if len(items) >= maxItems {
					overflow = append(overflow, asTooMany(item, len(items)+len(overflow)+1))
					continue
				}
				items = append(items, item)
			}
		}
	}
	return append(items, overflow...), nil
}

func expandChange(appID, waba string, raw json.RawMessage, ingestedAt time.Time) []Item {
	if len(raw) > maxItemBytes {
		return []Item{quarantine(appID, waba, "", ReasonItemTooLarge, clip(raw), ingestedAt, hashKey(ReasonItemTooLarge, raw, len(raw)))}
	}
	var change wireChange
	if err := json.Unmarshal(raw, &change); err != nil {
		return []Item{quarantine(appID, waba, "", ReasonInvalidJSON, clip(raw), ingestedAt, hashKey(ReasonInvalidJSON, raw, len(raw)))}
	}
	if change.Field != fieldMessages {
		phone := strings.TrimSpace(change.Value.Metadata.PhoneNumberID)
		if !digits(phone) {
			phone = ""
		}
		return []Item{quarantine(appID, waba, phone, ReasonUnsupportedField, clip(raw), ingestedAt, hashKey(ReasonUnsupportedField, raw, len(raw)))}
	}
	phone := strings.TrimSpace(change.Value.Metadata.PhoneNumberID)
	display := strings.TrimSpace(change.Value.Metadata.DisplayPhoneNumber)
	if phone == "" || !digits(phone) {
		return []Item{quarantine(appID, waba, "", ReasonMissingPhone, clip(raw), ingestedAt, hashKey(ReasonMissingPhone, raw, len(raw)))}
	}
	names := map[string]string{}
	for _, contact := range change.Value.Contacts {
		if id := strings.TrimSpace(contact.WaID); id != "" {
			names[id] = strings.TrimSpace(contact.Profile.Name)
		}
	}
	out := make([]Item, 0, len(change.Value.Messages)+len(change.Value.Statuses))
	for _, messageRaw := range change.Value.Messages {
		out = append(out, messageItem(appID, waba, phone, display, names, messageRaw, ingestedAt))
	}
	for _, statusRaw := range change.Value.Statuses {
		out = append(out, statusItem(appID, waba, phone, display, statusRaw, ingestedAt))
	}
	if len(out) == 0 {
		return []Item{quarantine(appID, waba, phone, ReasonMissingIdentity, clip(raw), ingestedAt, hashKey(ReasonMissingIdentity, raw, len(raw)))}
	}
	return out
}

// asTooMany keeps one excess item as quarantine. The preserved bytes are that
// item's object, not a dropped tail and not a second empty marker.
func asTooMany(item Item, seq int) Item {
	preserved := item.Preserved
	if len(preserved) == 0 {
		stub, err := json.Marshal(struct {
			Kind   string `json:"kind,omitempty"`
			ID     string `json:"external_id,omitempty"`
			Reason string `json:"reason,omitempty"`
		}{item.Kind, item.ExternalID, item.Reason})
		if err != nil {
			stub = []byte("null")
		}
		preserved = stub
	}
	preserved = clip(preserved)
	out := quarantine(item.AppID, item.WABAID, item.PhoneNumberID, ReasonTooMany, preserved, item.IngestedAt, hashKey(ReasonTooMany, preserved, seq))
	out.ExternalID = item.ExternalID
	out.DisplayPhone = item.DisplayPhone
	return out
}

func messageItem(appID, waba, phone, display string, names map[string]string, raw json.RawMessage, ingestedAt time.Time) Item {
	if len(raw) > maxItemBytes {
		return quarantine(appID, waba, phone, ReasonItemTooLarge, clip(raw), ingestedAt, hashKey(ReasonItemTooLarge, raw, len(raw)))
	}
	var msg wireMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return quarantine(appID, waba, phone, ReasonInvalidJSON, clip(raw), ingestedAt, hashKey(ReasonInvalidJSON, raw, len(raw)))
	}
	id := strings.TrimSpace(msg.ID)
	idOK := id != "" && !strings.ContainsAny(id, "\r\n\x00") && len(id) <= 256
	if strings.TrimSpace(msg.GroupID) != "" {
		return quarantineMessage(appID, waba, phone, display, id, idOK, ReasonGroupOutOfScope, raw, ingestedAt)
	}
	kind := strings.TrimSpace(msg.Type)
	if !supportedInbound(kind) {
		return quarantineMessage(appID, waba, phone, display, id, idOK, ReasonUnsupportedType, raw, ingestedAt)
	}
	from := strings.TrimSpace(msg.From)
	if !idOK || !wabaOK(waba) || from == "" {
		return quarantineMessage(appID, waba, phone, display, id, idOK, ReasonMissingIdentity, raw, ingestedAt)
	}
	content, text, err := decodeInbound(msg)
	if err != nil {
		return quarantineMessage(appID, waba, phone, display, id, idOK, ReasonInvalidJSON, raw, ingestedAt)
	}
	origin, known := parseUnix(msg.Timestamp)
	return Item{
		Kind:          ItemMessage,
		AppID:         appID,
		WABAID:        strings.TrimSpace(waba),
		PhoneNumberID: phone,
		DisplayPhone:  display,
		ExternalID:    id,
		DedupKey:      "message:" + id,
		OriginAt:      origin,
		OriginKnown:   known,
		IngestedAt:    ingestedAt,
		From:          from,
		Text:          text,
		ProfileName:   names[from],
		Content:       content,
		Preserved:     append([]byte(nil), raw...),
	}
}

func quarantineMessage(appID, waba, phone, display, id string, idOK bool, reason string, raw []byte, ingestedAt time.Time) Item {
	key := hashKey(reason, raw, len(raw))
	if idOK {
		key = "quarantine:" + reason + ":" + id
	}
	item := quarantine(appID, waba, phone, reason, clip(raw), ingestedAt, key)
	if idOK {
		item.ExternalID = id
	}
	item.DisplayPhone = display
	return item
}

func statusItem(appID, waba, phone, display string, raw json.RawMessage, ingestedAt time.Time) Item {
	if len(raw) > maxItemBytes {
		return quarantine(appID, waba, phone, ReasonItemTooLarge, clip(raw), ingestedAt, hashKey(ReasonItemTooLarge, raw, len(raw)))
	}
	var st wireStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return quarantine(appID, waba, phone, ReasonInvalidJSON, clip(raw), ingestedAt, hashKey(ReasonInvalidJSON, raw, len(raw)))
	}
	if strings.TrimSpace(st.RecipientType) == "group" {
		return quarantine(appID, waba, phone, ReasonGroupOutOfScope, clip(raw), ingestedAt, hashKey(ReasonGroupOutOfScope, raw, len(raw)))
	}
	id := strings.TrimSpace(st.ID)
	idOK := id != "" && !strings.ContainsAny(id, "\r\n\x00") && len(id) <= 256
	status := strings.TrimSpace(st.Status)
	switch status {
	case "sent", "delivered", "read", "failed", "played":
	default:
		key := hashKey(ReasonUnknownStatus, raw, len(raw))
		if idOK {
			key = "quarantine:" + ReasonUnknownStatus + ":" + id + ":" + status
		}
		item := quarantine(appID, waba, phone, ReasonUnknownStatus, clip(raw), ingestedAt, key)
		if idOK {
			item.ExternalID = id
		}
		item.DisplayPhone = display
		return item
	}
	if !idOK || !wabaOK(waba) {
		key := hashKey(ReasonMissingIdentity, raw, len(raw))
		if idOK {
			key = "quarantine:" + ReasonMissingIdentity + ":" + id
		}
		item := quarantine(appID, waba, phone, ReasonMissingIdentity, clip(raw), ingestedAt, key)
		if idOK {
			item.ExternalID = id
		}
		item.DisplayPhone = display
		return item
	}
	origin, known := parseUnix(st.Timestamp)
	remote := whatsapp.EvidenceNone
	switch status {
	case "delivered":
		remote = whatsapp.EvidenceDelivered
	case "read":
		remote = whatsapp.EvidenceRead
	}
	codes := make([]int, 0, len(st.Errors))
	for _, errItem := range st.Errors {
		if errItem.Code > 0 {
			codes = append(codes, errItem.Code)
		}
	}
	callback := ""
	if st.Callback != "" && len(st.Callback) <= MaxCallbackBytes && !strings.ContainsRune(st.Callback, 0) {
		callback = st.Callback
	}
	var billable *bool
	if st.Pricing.Billable != nil {
		value := *st.Pricing.Billable
		billable = &value
	}
	return Item{
		Kind:            ItemStatus,
		AppID:           appID,
		WABAID:          strings.TrimSpace(waba),
		PhoneNumberID:   phone,
		DisplayPhone:    display,
		ExternalID:      id,
		DedupKey:        statusDedup(st, codes, callback),
		OriginAt:        origin,
		OriginKnown:     known,
		IngestedAt:      ingestedAt,
		Status:          status,
		Remote:          remote,
		RecipientID:     strings.TrimSpace(st.RecipientID),
		CallbackData:    callback,
		ErrorCodes:      codes,
		PricingCategory: strings.TrimSpace(st.Pricing.Category),
		PricingType:     strings.TrimSpace(st.Pricing.Type),
		PricingModel:    strings.TrimSpace(st.Pricing.Model),
		Billable:        billable,
		Preserved:       append([]byte(nil), raw...),
	}
}

func statusDedup(st wireStatus, codes []int, callback string) string {
	key := statusKey{
		ID:        strings.TrimSpace(st.ID),
		Status:    strings.TrimSpace(st.Status),
		Timestamp: strings.TrimSpace(st.Timestamp),
		Recipient: strings.TrimSpace(st.RecipientID),
		Errors:    codes,
		Category:  strings.TrimSpace(st.Pricing.Category),
		Type:      strings.TrimSpace(st.Pricing.Type),
		Model:     strings.TrimSpace(st.Pricing.Model),
		Billable:  st.Pricing.Billable,
		Callback:  callback,
	}
	raw, err := json.Marshal(key)
	if err != nil {
		return "status:" + key.ID + ":" + key.Status + ":" + key.Timestamp
	}
	return "status:" + string(raw)
}

type statusKey struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Recipient string `json:"recipient_id,omitempty"`
	Errors    []int  `json:"errors,omitempty"`
	Category  string `json:"pricing_category,omitempty"`
	Type      string `json:"pricing_type,omitempty"`
	Model     string `json:"pricing_model,omitempty"`
	Billable  *bool  `json:"billable,omitempty"`
	Callback  string `json:"callback,omitempty"`
}

func quarantine(appID, waba, phone, reason string, preserved []byte, ingestedAt time.Time, key string) Item {
	return Item{
		Kind:          ItemQuarantine,
		AppID:         appID,
		WABAID:        strings.TrimSpace(waba),
		PhoneNumberID: strings.TrimSpace(phone),
		DedupKey:      key,
		IngestedAt:    ingestedAt,
		Preserved:     preserved,
		Reason:        reason,
	}
}

func hashKey(reason string, raw []byte, n int) string {
	sum := sha256.Sum256(raw)
	return "quarantine:" + reason + ":" + hex.EncodeToString(sum[:8]) + ":" + strconv.Itoa(n)
}

func wabaOK(waba string) bool {
	waba = strings.TrimSpace(waba)
	return waba != "" && len(waba) <= 128 && !strings.ContainsAny(waba, "\r\n\x00")
}

func clip(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxItemBytes {
		raw = raw[:maxItemBytes]
	}
	return append([]byte(nil), raw...)
}

func parseUnix(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 20 {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

type wireDoc struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string            `json:"id"`
		Changes []json.RawMessage `json:"changes"`
	} `json:"entry"`
}

type wireChange struct {
	Field string `json:"field"`
	Value struct {
		Metadata struct {
			DisplayPhoneNumber string `json:"display_phone_number"`
			PhoneNumberID      string `json:"phone_number_id"`
		} `json:"metadata"`
		Contacts []struct {
			WaID    string `json:"wa_id"`
			Profile struct {
				Name string `json:"name"`
			} `json:"profile"`
		} `json:"contacts"`
		Messages []json.RawMessage `json:"messages"`
		Statuses []json.RawMessage `json:"statuses"`
	} `json:"value"`
}

type wireMessage struct {
	From        string          `json:"from"`
	ID          string          `json:"id"`
	Timestamp   string          `json:"timestamp"`
	Type        string          `json:"type"`
	GroupID     string          `json:"group_id"`
	Context     json.RawMessage `json:"context"`
	Text        json.RawMessage `json:"text"`
	Image       json.RawMessage `json:"image"`
	Document    json.RawMessage `json:"document"`
	Audio       json.RawMessage `json:"audio"`
	Video       json.RawMessage `json:"video"`
	Sticker     json.RawMessage `json:"sticker"`
	Location    json.RawMessage `json:"location"`
	Contacts    json.RawMessage `json:"contacts"`
	Reaction    json.RawMessage `json:"reaction"`
	Interactive json.RawMessage `json:"interactive"`
	Button      json.RawMessage `json:"button"`
}

type wireStatus struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Timestamp     string `json:"timestamp"`
	RecipientID   string `json:"recipient_id"`
	RecipientType string `json:"recipient_type"`
	Callback      string `json:"biz_opaque_callback_data"`
	Errors        []struct {
		Code int `json:"code"`
	} `json:"errors"`
	Pricing struct {
		Billable *bool  `json:"billable"`
		Category string `json:"category"`
		Type     string `json:"type"`
		Model    string `json:"pricing_model"`
	} `json:"pricing"`
}
