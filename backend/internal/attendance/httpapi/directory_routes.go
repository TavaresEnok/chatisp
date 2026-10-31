package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// RegisterDirectory publishes the operational identity directory, distinct from
// the administrative /api/panel/memberships.
func RegisterDirectory(api huma.API, deps Dependencies) {
	h := handler{deps}
	huma.Register(api, huma.Operation{OperationID: "attendance-directory", Method: http.MethodGet, Path: "/api/panel/inboxes/{inbox_id}/directory", Tags: []string{"atendimento"}, DefaultStatus: 200, Errors: []int{401, 403, 404, 422, 503}}, h.directory)
	huma.Register(api, huma.Operation{OperationID: "attendance-transfer-sectors", Method: http.MethodGet, Path: "/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/transfer-sectors", Tags: []string{"atendimento"}, DefaultStatus: 200, Errors: []int{401, 403, 404, 422, 503}}, h.transferSectors)
}

func (h handler) transferSectors(ctx context.Context, in *ConversationInput) (*transferSectorsOutput, error) {
	p, err := h.principal(ctx, in.Credentials, false)
	if err != nil {
		return nil, err
	}
	v, err := h.deps.Attendance.TransferSectors(ctx, p, in.InboxID, in.ConversationID)
	if err != nil {
		return nil, problem(err)
	}
	return &transferSectorsOutput{Body: v}, nil
}

func (h handler) directory(ctx context.Context, in *directoryInput) (*directoryOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	var after *uuid.UUID
	if in.After != "" {
		id, err := uuid.Parse(in.After)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("cursor inválido")
		}
		after = &id
	}
	if in.Limit < 0 || in.Limit > 100 {
		return nil, huma.Error422UnprocessableEntity("limite inválido")
	}
	v, e := h.deps.Attendance.Directory(ctx, p, in.InboxID, after, in.Limit)
	if e != nil {
		return nil, problem(e)
	}
	return &directoryOutput{Body: v}, nil
}
