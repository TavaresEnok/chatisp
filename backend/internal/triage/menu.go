package triage

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	// DefaultMaxAttempts is the attempt budget when the inbox has no menu row.
	DefaultMaxAttempts = 3
	// MaxAttemptsLimit is the short ceiling stored for one inbox.
	MaxAttemptsLimit = 5
	// MaxOptions is the largest menu the admin can save.
	MaxOptions = 12

	// DestSector routes the chosen option to a sector queue.
	DestSector = "sector"
	// DestHuman leaves the menu and enters the human queue.
	DestHuman = "human"

	// KindSendText is a welcome or other plain text the composer may send later.
	KindSendText = "send_text"
	// KindSendMenu is the numbered fallback. The driver is not contacted.
	KindSendMenu = "send_menu"
	// KindRouteSector asks the composer to move the conversation to a sector.
	KindRouteSector = "route_sector"
	// KindForwardHuman is the mandatory human exit.
	KindForwardHuman = "forward_human"
	// KindRecordReason asks the composer to store the menu motive.
	KindRecordReason = "record_reason"
	// KindOutsideHours carries the closed-hours text.
	KindOutsideHours = "outside_hours"
	// KindOnCall routes to the authorized on-call sector.
	KindOnCall = "on_call"
	// KindNobodyAvailable carries the text used when the port reports that reason.
	KindNobodyAvailable = "nobody_available"

	// PhaseMenu means the customer is still choosing.
	PhaseMenu = "menu"
	// PhaseRouted means an option, the default sector or a human request was accepted.
	PhaseRouted = "routed"
	// PhaseExhausted means unexpected input reached the attempt limit.
	PhaseExhausted = "exhausted"
	// PhaseOutside means the inbox is closed and the destination is not on-call.
	PhaseOutside = "outside_hours"
	// PhaseOnCall means the inbox is closed and the on-call sector is the destination.
	PhaseOnCall = "on_call"
	// PhaseSkipped means this conversation is not in menu triage.
	PhaseSkipped = "skipped"
)

var keyPattern = regexp.MustCompile(`^[0-9]{1,4}$`)

// choiceSeparators separate a numeric key from its exact label.
// The first one is the line printed by the numbered fallback.
var choiceSeparators = []string{" - ", " ", "-", ".", ")", ":"}

// Option is one numbered choice. A human option has no sector and no motive.
type Option struct {
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	Destination string     `json:"destination"`
	SectorID    *uuid.UUID `json:"sector_id,omitempty"`
	Reason      string     `json:"reason"`
}

// Menu is the inbox triage configuration. Version 0 means there is no row.
type Menu struct {
	Version     int64    `json:"version"`
	Configured  bool     `json:"configured"`
	MaxAttempts int      `json:"max_attempts"`
	Options     []Option `json:"options"`
}

// Command replaces the whole menu. The same request id and body replay the stored response.
type Command struct {
	RequestID       uuid.UUID `json:"request_id"`
	ExpectedVersion int64     `json:"expected_version"`
	MaxAttempts     int       `json:"max_attempts"`
	Options         []Option  `json:"options"`
}

// Action is declarative. Nothing in this package delivers it to a channel.
type Action struct {
	Kind     string     `json:"kind"`
	Text     string     `json:"text,omitempty"`
	Fallback string     `json:"fallback,omitempty"`
	SectorID *uuid.UUID `json:"sector_id,omitempty"`
	Reason   string     `json:"reason,omitempty"`
	Options  []Option   `json:"options,omitempty"`
}

// Outcome is the decision stored for one input id.
// ControllerEpoch and Version are the conversation row locked for this decision.
// The composer drops the actions when either value no longer matches.
type Outcome struct {
	Actions         []Action `json:"actions"`
	Attempts        int      `json:"attempts"`
	WelcomeSent     bool     `json:"welcome_sent"`
	Phase           string   `json:"phase"`
	ControllerEpoch int64    `json:"controller_epoch"`
	Version         int64    `json:"version"`
}

// Validate checks the admin menu. A saved menu always contains a human option.
func Validate(maxAttempts int, options []Option) error {
	if maxAttempts < 1 || maxAttempts > MaxAttemptsLimit || len(options) == 0 || len(options) > MaxOptions {
		return ErrInvalid
	}
	keys := map[string]bool{}
	labels := map[string]bool{}
	human := false
	for _, option := range options {
		if !keyPattern.MatchString(option.Key) || option.Label == "" || option.Label != strings.TrimSpace(option.Label) {
			return ErrInvalid
		}
		if utf8.RuneCountInString(option.Label) > 80 || strings.ContainsRune(option.Label, 0) || strings.ContainsAny(option.Label, "\r\n") {
			return ErrInvalid
		}
		if option.Reason != strings.TrimSpace(option.Reason) || utf8.RuneCountInString(option.Reason) > 40 || strings.ContainsRune(option.Reason, 0) || strings.ContainsAny(option.Reason, "\r\n") {
			return ErrInvalid
		}
		key := strings.ToLower(option.Key)
		label := strings.ToLower(option.Label)
		if keys[key] || labels[label] {
			return ErrInvalid
		}
		keys[key] = true
		labels[label] = true
		if option.Destination == DestSector && reservedHuman(label) {
			return ErrInvalid
		}
		switch option.Destination {
		case DestHuman:
			if option.SectorID != nil || option.Reason != "" {
				return ErrInvalid
			}
			human = true
		case DestSector:
			if option.SectorID == nil || *option.SectorID == uuid.Nil {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	forms := map[string]string{}
	for _, option := range options {
		key := strings.ToLower(option.Key)
		for _, form := range choiceForms(option) {
			if owner, seen := forms[form]; seen && owner != key {
				return ErrInvalid
			}
			forms[form] = key
		}
	}
	if !human {
		return ErrInvalid
	}
	return nil
}

func choiceForms(option Option) []string {
	key := strings.ToLower(option.Key)
	label := strings.ToLower(option.Label)
	forms := make([]string, 0, 2+len(choiceSeparators))
	forms = append(forms, key, label)
	for _, sep := range choiceSeparators {
		forms = append(forms, key+sep+label)
	}
	return forms
}

func normalizeMenu(menu Menu) Menu {
	if menu.Options == nil {
		menu.Options = []Option{}
	}
	if menu.MaxAttempts == 0 {
		menu.MaxAttempts = DefaultMaxAttempts
	}
	return menu
}
