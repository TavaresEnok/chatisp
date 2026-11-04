package metacloud

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// errMalformed rejects a supported type whose object cannot be read.
var errMalformed = errors.New("payload malformado")

// InboundContent is the structured body of one supported inbound message.
// Reaction is not a new attendance message; the pipeline attaches it to the target.
type InboundContent struct {
	Type            string        `json:"type,omitempty"`
	Caption         string        `json:"caption,omitempty"`
	Filename        string        `json:"filename,omitempty"`
	MIME            string        `json:"mime,omitempty"`
	MediaID         string        `json:"media_id,omitempty"`
	Voice           bool          `json:"voice,omitempty"`
	Latitude        *float64      `json:"latitude,omitempty"`
	Longitude       *float64      `json:"longitude,omitempty"`
	LocationName    string        `json:"location_name,omitempty"`
	Address         string        `json:"address,omitempty"`
	Contacts        []ContactCard `json:"contacts,omitempty"`
	Emoji           string        `json:"emoji,omitempty"`
	ReactionTarget  string        `json:"reaction_target,omitempty"`
	InteractiveType string        `json:"interactive_type,omitempty"`
	ReplyID         string        `json:"reply_id,omitempty"`
	Title           string        `json:"title,omitempty"`
	ContextID       string        `json:"context_id,omitempty"`
	MediaStatus     string        `json:"media_status,omitempty"`
	MediaError      string        `json:"media_error,omitempty"`
	SniffedMIME     string        `json:"sniffed_mime,omitempty"`
	AttachmentID    string        `json:"attachment_id,omitempty"`
	ReplyToMessage  string        `json:"reply_to_message_id,omitempty"`
	Bytes           int64         `json:"bytes,omitempty"`
	ReserveToken    string        `json:"reserve_token,omitempty"`
	ReservedAt      string        `json:"reserved_at,omitempty"`
}

// ContactCard is one normalized WhatsApp contact.
type ContactCard struct {
	Name   string   `json:"name,omitempty"`
	Phones []string `json:"phones,omitempty"`
	Emails []string `json:"emails,omitempty"`
	Org    string   `json:"org,omitempty"`
	VCard  string   `json:"vcard,omitempty"`
}

func supportedInbound(kind string) bool {
	switch kind {
	case "text", "image", "document", "audio", "video", "sticker", "location", "contacts", "reaction", "interactive", "button":
		return true
	default:
		return false
	}
}

func decodeInbound(msg wireMessage) (InboundContent, string, error) {
	contextID, err := contextID(msg.Context)
	if err != nil {
		return InboundContent{}, "", err
	}
	var content InboundContent
	var text string
	switch strings.TrimSpace(msg.Type) {
	case "text":
		text, err = decodeText(msg.Text)
		content.Type = "text"
	case "image":
		content, err = decodeMedia("image", msg.Image, true)
		text = content.Caption
	case "document":
		content, err = decodeMedia("document", msg.Document, true)
		text = firstText(content.Caption, content.Filename)
	case "audio":
		content, err = decodeMedia("audio", msg.Audio, false)
		if err == nil && content.Voice {
			content.Type = "voice"
		}
	case "video":
		content, err = decodeMedia("video", msg.Video, true)
		text = content.Caption
	case "sticker":
		content, err = decodeMedia("sticker", msg.Sticker, false)
	case "location":
		content, text, err = decodeLocation(msg.Location)
	case "contacts":
		content, text, err = decodeContacts(msg.Contacts)
	case "reaction":
		content, err = decodeReaction(msg.Reaction)
	case "interactive":
		content, text, err = decodeInteractive(msg.Interactive)
	case "button":
		content, text, err = decodeButton(msg.Button)
	default:
		return InboundContent{}, "", errMalformed
	}
	if err != nil {
		return InboundContent{}, "", err
	}
	content.ContextID = contextID
	return content, text, nil
}

func contextID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", errMalformed
	}
	id := strings.TrimSpace(doc.ID)
	if id == "" {
		return "", nil
	}
	if !idOK(id) {
		return "", errMalformed
	}
	return id, nil
}

func decodeText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var doc struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", errMalformed
	}
	return clipRunes(doc.Body, 16000), nil
}

func decodeMedia(kind string, raw json.RawMessage, withCaption bool) (InboundContent, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return InboundContent{}, errMalformed
	}
	var doc struct {
		ID       string `json:"id"`
		MIME     string `json:"mime_type"`
		Caption  string `json:"caption"`
		Filename string `json:"filename"`
		Voice    bool   `json:"voice"`
		SHA256   string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return InboundContent{}, errMalformed
	}
	id := strings.TrimSpace(doc.ID)
	if !idOK(id) {
		return InboundContent{}, errMalformed
	}
	out := InboundContent{
		Type:     kind,
		MediaID:  id,
		MIME:     clipToken(doc.MIME, 200),
		Voice:    doc.Voice,
		Filename: clipToken(doc.Filename, 180),
	}
	if withCaption {
		out.Caption = clipRunes(doc.Caption, 4096)
	}
	return out, nil
}

func decodeLocation(raw json.RawMessage) (InboundContent, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return InboundContent{}, "", errMalformed
	}
	var doc struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
		Name      string   `json:"name"`
		Address   string   `json:"address"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Latitude == nil || doc.Longitude == nil {
		return InboundContent{}, "", errMalformed
	}
	if *doc.Latitude < -90 || *doc.Latitude > 90 || *doc.Longitude < -180 || *doc.Longitude > 180 {
		return InboundContent{}, "", errMalformed
	}
	name := clipRunes(doc.Name, 256)
	address := clipRunes(doc.Address, 512)
	lat := *doc.Latitude
	lng := *doc.Longitude
	text := strconv.FormatFloat(lat, 'f', -1, 64) + "," + strconv.FormatFloat(lng, 'f', -1, 64)
	if name != "" {
		text = name
	}
	return InboundContent{
		Type:         "location",
		Latitude:     &lat,
		Longitude:    &lng,
		LocationName: name,
		Address:      address,
	}, text, nil
}

func decodeContacts(raw json.RawMessage) (InboundContent, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return InboundContent{}, "", errMalformed
	}
	var docs []struct {
		Name struct {
			Formatted string `json:"formatted_name"`
			First     string `json:"first_name"`
		} `json:"name"`
		Phones []struct {
			Phone string `json:"phone"`
		} `json:"phones"`
		Emails []struct {
			Email string `json:"email"`
		} `json:"emails"`
		Org struct {
			Company string `json:"company"`
		} `json:"org"`
	}
	if err := json.Unmarshal(raw, &docs); err != nil || len(docs) == 0 || len(docs) > 20 {
		return InboundContent{}, "", errMalformed
	}
	cards := make([]ContactCard, 0, len(docs))
	names := make([]string, 0, len(docs))
	for _, doc := range docs {
		name := clipRunes(firstText(doc.Name.Formatted, doc.Name.First), 200)
		phones := make([]string, 0, len(doc.Phones))
		for _, phone := range doc.Phones {
			if value := clipToken(phone.Phone, 40); value != "" && len(phones) < 5 {
				phones = append(phones, value)
			}
		}
		emails := make([]string, 0, len(doc.Emails))
		for _, email := range doc.Emails {
			if value := clipToken(email.Email, 200); value != "" && len(emails) < 5 {
				emails = append(emails, value)
			}
		}
		org := clipRunes(doc.Org.Company, 200)
		if name == "" && len(phones) == 0 && len(emails) == 0 {
			return InboundContent{}, "", errMalformed
		}
		card := ContactCard{Name: name, Phones: phones, Emails: emails, Org: org, VCard: buildVCard(name, phones, emails, org)}
		cards = append(cards, card)
		if name != "" {
			names = append(names, name)
		}
	}
	return InboundContent{Type: "contacts", Contacts: cards}, strings.Join(names, ", "), nil
}

func decodeReaction(raw json.RawMessage) (InboundContent, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return InboundContent{}, errMalformed
	}
	var doc struct {
		MessageID string `json:"message_id"`
		Emoji     string `json:"emoji"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return InboundContent{}, errMalformed
	}
	target := strings.TrimSpace(doc.MessageID)
	if !idOK(target) {
		return InboundContent{}, errMalformed
	}
	emoji := strings.TrimSpace(doc.Emoji)
	if utf8.RuneCountInString(emoji) > 32 || strings.ContainsAny(emoji, "\r\n\x00") {
		return InboundContent{}, errMalformed
	}
	return InboundContent{Type: "reaction", Emoji: emoji, ReactionTarget: target}, nil
}

func decodeInteractive(raw json.RawMessage) (InboundContent, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return InboundContent{}, "", errMalformed
	}
	var doc struct {
		Type        string `json:"type"`
		ButtonReply struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"button_reply"`
		ListReply struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"list_reply"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return InboundContent{}, "", errMalformed
	}
	kind := strings.TrimSpace(doc.Type)
	var id, title string
	switch kind {
	case "button_reply":
		id, title = doc.ButtonReply.ID, doc.ButtonReply.Title
	case "list_reply":
		id, title = doc.ListReply.ID, doc.ListReply.Title
	default:
		return InboundContent{}, "", errMalformed
	}
	id = strings.TrimSpace(id)
	title = clipRunes(title, 256)
	if !idOK(id) {
		return InboundContent{}, "", errMalformed
	}
	return InboundContent{Type: "interactive", InteractiveType: kind, ReplyID: id, Title: title}, title, nil
}

func decodeButton(raw json.RawMessage) (InboundContent, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return InboundContent{}, "", errMalformed
	}
	var doc struct {
		Payload string `json:"payload"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return InboundContent{}, "", errMalformed
	}
	id := strings.TrimSpace(doc.Payload)
	title := clipRunes(doc.Text, 256)
	if id == "" && title == "" {
		return InboundContent{}, "", errMalformed
	}
	if id != "" && !idOK(id) {
		return InboundContent{}, "", errMalformed
	}
	text := title
	if text == "" {
		text = id
	}
	return InboundContent{Type: "button", ReplyID: id, Title: title}, text, nil
}

func buildVCard(name string, phones, emails []string, org string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\nVERSION:3.0\n")
	if name != "" {
		b.WriteString("FN:")
		b.WriteString(vcardEscape(name))
		b.WriteByte('\n')
	}
	for _, phone := range phones {
		b.WriteString("TEL:")
		b.WriteString(vcardEscape(phone))
		b.WriteByte('\n')
	}
	for _, email := range emails {
		b.WriteString("EMAIL:")
		b.WriteString(vcardEscape(email))
		b.WriteByte('\n')
	}
	if org != "" {
		b.WriteString("ORG:")
		b.WriteString(vcardEscape(org))
		b.WriteByte('\n')
	}
	b.WriteString("END:VCARD")
	return b.String()
}

func vcardEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, ";", `\;`)
	value = strings.ReplaceAll(value, ",", `\,`)
	return value
}

func idOK(id string) bool {
	return id != "" && len(id) <= 256 && !strings.ContainsAny(id, "\r\n\x00")
}

func clipRunes(value string, max int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}

func clipToken(value string, max int) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
