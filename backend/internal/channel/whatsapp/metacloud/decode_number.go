package metacloud

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	// ItemNumberLimit is one phone_number_quality_update change.
	// The official payload carries the WABA (entry.id) and the display number,
	// never the phone_number_id. Routing to a number is the caller's job.
	ItemNumberLimit = "number_limit"

	// ReasonInvalidLimit marks a phone_number_quality_update this decoder
	// cannot trust: missing WABA, display number or event, or a value outside
	// the documented shape.
	ReasonInvalidLimit = "limite_invalido"

	fieldNumberLimit = "phone_number_quality_update"

	maxTokenLen = 64
	maxDaily    = 1_000_000_000
)

// NumberLimit is the decoded value of phone_number_quality_update.
// Fields are kept as Meta sends them. Tier names are not translated here
// and are not mapped to a message count: the documented list may change.
type NumberLimit struct {
	Event        string
	CurrentLimit string
	OldLimit     string
	MaxDaily     *int64
}

type wireNumberLimit struct {
	Value struct {
		DisplayPhoneNumber string      `json:"display_phone_number"`
		Event              string      `json:"event"`
		CurrentLimit       string      `json:"current_limit"`
		OldLimit           string      `json:"old_limit"`
		MaxDaily           json.Number `json:"max_daily_conversations_per_business"`
	} `json:"value"`
}

func numberLimitItem(appID, waba string, raw json.RawMessage, ingestedAt time.Time) Item {
	sum := sha256.Sum256(raw)
	key := "number_limit:" + hex.EncodeToString(sum[:16])
	bad := func() Item {
		return quarantine(appID, waba, "", ReasonInvalidLimit, clip(raw), ingestedAt, hashKey(ReasonInvalidLimit, raw, len(raw)))
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var wire wireNumberLimit
	if err := dec.Decode(&wire); err != nil {
		return bad()
	}
	display := DisplayDigits(wire.Value.DisplayPhoneNumber)
	if !wabaOK(waba) || display == "" {
		return bad()
	}
	event := strings.TrimSpace(wire.Value.Event)
	current := strings.TrimSpace(wire.Value.CurrentLimit)
	old := strings.TrimSpace(wire.Value.OldLimit)
	if !upperToken(event) || (current != "" && !upperToken(current)) || (old != "" && !upperToken(old)) {
		return bad()
	}
	var daily *int64
	if s := strings.TrimSpace(wire.Value.MaxDaily.String()); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 || n > maxDaily {
			return bad()
		}
		daily = &n
	}
	if current == "" && daily == nil {
		return bad()
	}
	return Item{
		Kind:         ItemNumberLimit,
		AppID:        appID,
		WABAID:       strings.TrimSpace(waba),
		DisplayPhone: display,
		DedupKey:     key,
		IngestedAt:   ingestedAt,
		Limit:        NumberLimit{Event: event, CurrentLimit: current, OldLimit: old, MaxDaily: daily},
	}
}

// DisplayDigits keeps only the digits of a display number. It returns ""
// when the result is not a plausible E.164 length (8 to 15 digits).
func DisplayDigits(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
		default:
			return ""
		}
	}
	out := b.String()
	if len(out) < 8 || len(out) > 15 {
		return ""
	}
	return out
}

func upperToken(value string) bool {
	if value == "" || len(value) > maxTokenLen {
		return false
	}
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
