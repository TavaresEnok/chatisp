package store

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"chatisp/internal/platform/db/dbgen"
)

func conversationFrom(row dbgen.Conversation) Conversation {
	return Conversation{
		ID:                       row.ID,
		TenantID:                 row.TenantID,
		InboxID:                  uuidVal(row.InboxID),
		SectorID:                 uuidVal(row.SectorID),
		ContactID:                uuidVal(row.ContactID),
		IdentityID:               uuidVal(row.ContactIdentityID),
		Status:                   row.Status,
		Controller:               row.Controller,
		ControllerEpoch:          row.ControllerEpoch,
		Version:                  row.Version,
		AssigneeID:               uuidVal(row.AssigneeID),
		Protocol:                 strVal(row.Protocol),
		ProtocolPeriod:           strVal(row.ProtocolPeriod),
		ProtocolCommunicated:     row.ProtocolCommunicated,
		ExternalProtocol:         strVal(row.ExternalProtocol),
		LastCustomerMessageAt:    timeOf(row.LastCustomerMessageAt),
		LastEligibleHumanReplyAt: timeOf(row.LastEligibleHumanReplyAt),
		WaitingSince:             timeOf(row.WaitingSince),
		CycleNumber:              row.CycleNumber,
		Summary:                  row.Summary,
		CreatedAt:                timeOf(row.CreatedAt),
	}
}

func messageFrom(row dbgen.ListMessagesRow) Message {
	return Message{
		ID:                row.ID,
		ConversationID:    row.ConversationID,
		ExternalMessageID: strVal(row.ExternalMessageID),
		Direction:         row.Direction,
		Origin:            row.Origin,
		Visibility:        row.Visibility,
		Body:              row.Body,
		LocalState:        strVal(row.LocalState),
		TrustedAt:         timeOf(row.TrustedAt),
		IngestedAt:        timeOf(row.IngestedAt),
		Historical:        row.Historical,
	}
}

func timeOf(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func ts(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func strPtr(value string) *string {
	return &value
}

func uuidVal(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func strVal(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// requestHash binds the idempotency key to the conversation, the connector,
// the visible body and the canonical payload. Length prefixes keep the fields apart.
func requestHash(conversationID, connectorID uuid.UUID, body string, payload []byte) []byte {
	mac := sha256.New()
	var n [8]byte
	write := func(part []byte) {
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		mac.Write(n[:])
		mac.Write(part)
	}
	write(conversationID[:])
	write(connectorID[:])
	write([]byte(body))
	write(payload)
	return mac.Sum(nil)
}

func formatProtocol(period string, n int64) string {
	if n <= 999999 {
		return period + "-" + leftPad(n, 6)
	}
	return period + "-" + strconv.FormatInt(n, 10)
}

func leftPad(n int64, width int) string {
	raw := strconv.FormatInt(n, 10)
	if len(raw) >= width {
		return raw
	}
	return strings.Repeat("0", width-len(raw)) + raw
}

func validPeriod(period string) bool {
	if len(period) != 6 {
		return false
	}
	for _, r := range period {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validResult(result string) bool {
	switch result {
	case ResultResolved, ResultNoResponse, ResultReferredExternal, ResultSpam:
		return true
	default:
		return false
	}
}

func text(value string, max int) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max {
		return "", false
	}
	return value, true
}
