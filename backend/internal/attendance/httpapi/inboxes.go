package httpapi

import (
	"chatisp/internal/attendance/service"
	"context"
)

type inboxesInput struct{ Credentials }
type inboxesOutput struct {
	Body struct {
		Inboxes []service.Inbox `json:"inboxes"`
	}
}

func (h handler) inboxes(ctx context.Context, in *inboxesInput) (*inboxesOutput, error) {
	p, err := h.principal(ctx, in.Credentials, false)
	if err != nil {
		return nil, err
	}
	inboxes, err := h.deps.Attendance.ListInboxes(ctx, p)
	if err != nil {
		return nil, problem(err)
	}
	out := &inboxesOutput{}
	out.Body.Inboxes = inboxes
	return out, nil
}
