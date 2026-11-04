package metacloud

import "encoding/json"

// TextFixture is one inbound text message inside a reusable webhook body.
// It is protocol-shaped sample data, not a live Meta payload.
type TextFixture struct {
	WABAID        string
	PhoneNumberID string
	DisplayPhone  string
	MessageID     string
	From          string
	WaID          string
	ProfileName   string
	Body          string
	Timestamp     string
}

// StatusFixture is one status object inside a reusable webhook body.
type StatusFixture struct {
	WABAID          string
	PhoneNumberID   string
	DisplayPhone    string
	MessageID       string
	Status          string
	Timestamp       string
	RecipientID     string
	CallbackData    string
	PricingCategory string
	PricingType     string
	PricingModel    string
	Billable        *bool
	ErrorCode       int
}

type changeGroup struct {
	waba    string
	phone   string
	display string
	value   map[string]any
}

// MarshalTextEnvelope builds one webhook body. Items that share a WABA and
// phone number stay in that number's change. The result has no tenant.
func MarshalTextEnvelope(items ...TextFixture) ([]byte, error) {
	groups := make([]changeGroup, 0)
	index := map[string]int{}
	for _, item := range items {
		if item.WABAID == "" || item.PhoneNumberID == "" || item.MessageID == "" {
			return nil, errPhoneMissing
		}
		id := item.WABAID + "\x00" + item.PhoneNumberID
		n, ok := index[id]
		if !ok {
			n = len(groups)
			index[id] = n
			groups = append(groups, changeGroup{
				waba: item.WABAID, phone: item.PhoneNumberID, display: item.DisplayPhone,
				value: map[string]any{
					"messaging_product": "whatsapp",
					"metadata": map[string]string{
						"display_phone_number": item.DisplayPhone,
						"phone_number_id":      item.PhoneNumberID,
					},
					"contacts": []any{},
					"messages": []any{},
				},
			})
		}
		messages := groups[n].value["messages"].([]any)
		groups[n].value["messages"] = append(messages, map[string]any{
			"from":      item.From,
			"id":        item.MessageID,
			"timestamp": item.Timestamp,
			"type":      "text",
			"text":      map[string]string{"body": item.Body},
		})
		wa := item.WaID
		if wa == "" {
			wa = item.From
		}
		if wa == "" {
			continue
		}
		contacts := groups[n].value["contacts"].([]any)
		known := false
		for _, existing := range contacts {
			if existing.(map[string]any)["wa_id"] == wa {
				known = true
				break
			}
		}
		if !known {
			groups[n].value["contacts"] = append(contacts, map[string]any{
				"profile": map[string]string{"name": item.ProfileName},
				"wa_id":   wa,
			})
		}
	}
	return marshalGroups(groups)
}

// MarshalStatusEnvelope builds one webhook body of status objects.
// Equal fixtures stay duplicated so the caller can test the dedup key.
func MarshalStatusEnvelope(items ...StatusFixture) ([]byte, error) {
	groups := make([]changeGroup, 0)
	index := map[string]int{}
	for _, item := range items {
		if item.WABAID == "" || item.PhoneNumberID == "" || item.MessageID == "" {
			return nil, errPhoneMissing
		}
		id := item.WABAID + "\x00" + item.PhoneNumberID
		n, ok := index[id]
		if !ok {
			n = len(groups)
			index[id] = n
			groups = append(groups, changeGroup{
				waba: item.WABAID, phone: item.PhoneNumberID, display: item.DisplayPhone,
				value: map[string]any{
					"messaging_product": "whatsapp",
					"metadata": map[string]string{
						"display_phone_number": item.DisplayPhone,
						"phone_number_id":      item.PhoneNumberID,
					},
					"statuses": []any{},
				},
			})
		}
		object := map[string]any{
			"id":           item.MessageID,
			"status":       item.Status,
			"timestamp":    item.Timestamp,
			"recipient_id": item.RecipientID,
		}
		if item.CallbackData != "" {
			object["biz_opaque_callback_data"] = item.CallbackData
		}
		if item.PricingCategory != "" || item.PricingType != "" || item.PricingModel != "" || item.Billable != nil {
			pricing := map[string]any{}
			if item.Billable != nil {
				pricing["billable"] = *item.Billable
			}
			if item.PricingModel != "" {
				pricing["pricing_model"] = item.PricingModel
			}
			if item.PricingType != "" {
				pricing["type"] = item.PricingType
			}
			if item.PricingCategory != "" {
				pricing["category"] = item.PricingCategory
			}
			object["pricing"] = pricing
		}
		if item.ErrorCode > 0 {
			object["errors"] = []any{map[string]any{"code": item.ErrorCode}}
		}
		statuses := groups[n].value["statuses"].([]any)
		groups[n].value["statuses"] = append(statuses, object)
	}
	return marshalGroups(groups)
}

func marshalGroups(groups []changeGroup) ([]byte, error) {
	entries := make([]any, 0)
	entryAt := map[string]int{}
	for _, group := range groups {
		n, ok := entryAt[group.waba]
		if !ok {
			n = len(entries)
			entryAt[group.waba] = n
			entries = append(entries, map[string]any{"id": group.waba, "changes": []any{}})
		}
		entry := entries[n].(map[string]any)
		changes := entry["changes"].([]any)
		entry["changes"] = append(changes, map[string]any{
			"value": group.value,
			"field": "messages",
		})
	}
	return json.Marshal(map[string]any{
		"object": objectWhatsApp,
		"entry":  entries,
	})
}

// MessageFixture is one inbound message of any supported Cloud API type.
// Object is the typed payload (image, location, contacts, and so on).
type MessageFixture struct {
	WABAID        string
	PhoneNumberID string
	DisplayPhone  string
	MessageID     string
	From          string
	ProfileName   string
	Timestamp     string
	Type          string
	ContextID     string
	Object        any
}

// MarshalMessageEnvelope builds a webhook body from protocol-shaped fixtures.
func MarshalMessageEnvelope(items ...MessageFixture) ([]byte, error) {
	groups := make([]changeGroup, 0)
	index := map[string]int{}
	for _, item := range items {
		if item.WABAID == "" || item.PhoneNumberID == "" || item.MessageID == "" || item.Type == "" {
			return nil, errPhoneMissing
		}
		id := item.WABAID + "\x00" + item.PhoneNumberID
		n, ok := index[id]
		if !ok {
			n = len(groups)
			index[id] = n
			groups = append(groups, changeGroup{
				waba: item.WABAID, phone: item.PhoneNumberID, display: item.DisplayPhone,
				value: map[string]any{
					"messaging_product": "whatsapp",
					"metadata": map[string]string{
						"display_phone_number": item.DisplayPhone,
						"phone_number_id":      item.PhoneNumberID,
					},
					"contacts": []any{},
					"messages": []any{},
				},
			})
		}
		message := map[string]any{
			"from": item.From, "id": item.MessageID, "timestamp": item.Timestamp, "type": item.Type,
		}
		if item.Object != nil {
			message[item.Type] = item.Object
		}
		if item.ContextID != "" {
			message["context"] = map[string]string{"id": item.ContextID}
		}
		messages := groups[n].value["messages"].([]any)
		groups[n].value["messages"] = append(messages, message)
		if item.From == "" {
			continue
		}
		contacts := groups[n].value["contacts"].([]any)
		groups[n].value["contacts"] = append(contacts, map[string]any{
			"profile": map[string]string{"name": item.ProfileName},
			"wa_id":   item.From,
		})
	}
	return marshalGroups(groups)
}
