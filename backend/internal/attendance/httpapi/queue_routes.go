package httpapi

import (
	"context"
	"net/http"
	"time"

	"chatisp/internal/attendance/store"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// RegisterQueue is called by the composition root during feature integration.
func RegisterQueue(api huma.API, deps Dependencies) {
	h := handler{deps}
	huma.Register(api, huma.Operation{OperationID: "attendance-queue", Method: http.MethodGet, Path: "/api/panel/queue", Tags: []string{"atendimento"}, DefaultStatus: 200, Errors: []int{401, 403, 409, 422, 503}}, h.queue)
}
func (h handler) queue(ctx context.Context, in *queueInput) (*queueOutput, error) {
	p, err := h.principal(ctx, in.Credentials, false)
	if err != nil {
		return nil, err
	}
	q := store.QueueInput{Tab: in.Tab, Order: in.Order, Status: in.Status, Search: in.Search, Limit: in.Limit, Cursor: in.Cursor}
	for _, v := range []struct {
		raw string
		dst *uuid.UUID
	}{{in.Sector, &q.SectorID}, {in.Assignee, &q.AssigneeID}, {in.Inbox, &q.InboxID}, {in.Tag, &q.TagID}} {
		if v.raw != "" {
			*v.dst, err = uuid.Parse(v.raw)
			if err != nil || *v.dst == uuid.Nil {
				return nil, huma.Error422UnprocessableEntity("identificador inválido")
			}
		}
	}
	for _, v := range []struct {
		raw string
		dst **time.Time
	}{{in.From, &q.From}, {in.To, &q.To}} {
		if v.raw != "" {
			t, e := time.Parse("2006-01-02", v.raw)
			if e != nil {
				return nil, huma.Error422UnprocessableEntity("data inválida")
			}
			*v.dst = &t
		}
	}
	// The final calendar day is inclusive at the HTTP boundary.
	if q.To != nil {
		t := q.To.AddDate(0, 0, 1)
		q.To = &t
	}
	page, err := h.deps.Attendance.Queue(ctx, p, q)
	if err != nil {
		return nil, problem(err)
	}
	return &queueOutput{Body: page}, nil
}
