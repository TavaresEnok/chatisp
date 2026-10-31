package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"github.com/danielgtaylor/huma/v2"
)

// RegisterAssignment is called by the composition root when this feature is integrated.
func RegisterAssignment(api huma.API, deps Dependencies) {
	h := handler{deps}
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"atendimento"}, DefaultStatus: 200, MaxBodyBytes: 16384, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	item := "/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}"
	registerBody(api, op("assignment-member", http.MethodPost, item+"/transfer-member"), assignmentBody{}, h.transferMember)
	registerBody(api, op("assignment-sector", http.MethodPost, item+"/transfer-sector"), assignmentBody{}, h.transferSector)
	registerBody(api, op("assignment-unassign", http.MethodPost, item+"/unassign"), assignmentBody{}, h.unassign)
	huma.Register(api, op("assignment-candidates", http.MethodGet, item+"/assignment-candidates"), h.assignmentCandidates)
	registerBody(api, op("assignment-capacity", http.MethodPost, "/api/panel/memberships/{membership_id}/assignment-capacity"), capacityBody{}, h.assignmentCapacity)
}
func assignmentProblem(err error) error {
	if errors.Is(err, store.ErrCapacityFull) {
		return huma.Error409Conflict("capacidade esgotada")
	}
	return problem(err)
}
func (h handler) assignment(ctx context.Context, in *assignmentInput, kind string) (*conversationOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b assignmentBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	arg := service.AssignmentInput{InboxID: in.InboxID, ConversationID: in.ConversationID, TargetID: b.TargetID, ExpectedVersion: b.ExpectedVersion, ClientRequestID: b.ClientRequestID, At: time.Now().UTC()}
	var v store.Conversation
	switch kind {
	case "member":
		v, e = h.deps.Attendance.TransferToMember(ctx, p, arg)
	case "sector":
		v, e = h.deps.Attendance.TransferToSector(ctx, p, arg)
	default:
		v, e = h.deps.Attendance.Unassign(ctx, p, arg)
	}
	if e != nil {
		return nil, assignmentProblem(e)
	}
	return &conversationOutput{Body: conversationFrom(v)}, nil
}
func (h handler) transferMember(ctx context.Context, in *assignmentInput) (*conversationOutput, error) {
	return h.assignment(ctx, in, "member")
}
func (h handler) transferSector(ctx context.Context, in *assignmentInput) (*conversationOutput, error) {
	return h.assignment(ctx, in, "sector")
}
func (h handler) unassign(ctx context.Context, in *assignmentInput) (*conversationOutput, error) {
	return h.assignment(ctx, in, "unassign")
}
func (h handler) assignmentCandidates(ctx context.Context, in *ConversationInput) (*candidatesOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.AssignmentCandidates(ctx, p, in.InboxID, in.ConversationID)
	if e != nil {
		return nil, problem(e)
	}
	o := &candidatesOutput{}
	o.Body.Candidates = v
	return o, nil
}
func (h handler) assignmentCapacity(ctx context.Context, in *capacityInput) (*capacityOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b capacityBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	if e = h.deps.Attendance.SetAssignmentCapacity(ctx, p, in.MembershipID, b.Capacity); e != nil {
		return nil, problem(e)
	}
	o := &capacityOutput{}
	o.Body.Capacity = b.Capacity
	return o, nil
}
