// Package httpapi provides authenticated panel attendance operations.
package httpapi

import (
	"chatisp/internal/attendance/service"
	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/auth"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"net/http"
	"reflect"
	"time"
)

type Dependencies struct {
	Auth           *auth.Service
	Attendance     service.Service
	TrustedOrigins []string
}
type handler struct{ deps Dependencies }

func Register(api huma.API, deps Dependencies) {
	h := handler{deps}
	op := func(id, method, path string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"atendimento"}, DefaultStatus: status, MaxBodyBytes: 16384, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	base := "/api/panel/inboxes/{inbox_id}/conversations"
	item := base + "/{conversation_id}"
	huma.Register(api, op("attendance-inboxes", http.MethodGet, "/api/panel/inboxes", 200), h.inboxes)
	huma.Register(api, op("attendance-snapshot", http.MethodGet, base, 200), h.snapshot)
	huma.Register(api, op("attendance-history", http.MethodGet, item+"/history", 200), h.history)
	huma.Register(api, op("attendance-events", http.MethodGet, "/api/panel/inboxes/{inbox_id}/events", 200), h.events)
	registerBody(api, op("attendance-assume", http.MethodPost, item+"/assume", 200), versionBody{}, h.assume)
	registerBody(api, op("attendance-send", http.MethodPost, item+"/messages", 201), sendBody{}, h.send)
	registerBody(api, op("attendance-send-template", http.MethodPost, item+"/template-messages", 201), templateBody{}, h.sendTemplate)
	registerBody(api, op("attendance-close", http.MethodPost, item+"/close", 200), closeBody{}, h.close)
	registerBody(api, op("attendance-reopen", http.MethodPost, item+"/reopen", 200), reopenBody{}, h.reopen)
	RegisterAssignment(api, deps)
	// candidatesOutput carries an interface at runtime; publish its concrete wire type.
	registry := api.OpenAPI().Components.Schemas
	candidateBody := api.OpenAPI().Paths[item+"/assignment-candidates"].Get.Responses["200"].Content["application/json"].Schema
	if candidateBody.Ref != "" {
		candidateBody = registry.SchemaFromRef(candidateBody.Ref)
	}
	candidateBody.Properties["candidates"] = registry.Schema(reflect.TypeOf([]service.AssignmentCandidate{}), true, "AssignmentCandidates")
	RegisterAnnotations(api, deps)
	// The shared tag input has RawBody for PATCH; DELETE does not need it.
	// Huma retains this operation for request processing as well as OpenAPI.
	api.OpenAPI().Paths["/api/panel/annotation-tags/{tag_id}"].Delete.RequestBody.Required = false
	RegisterQueue(api, deps)
	RegisterDirectory(api, deps)
	// QueueItem always serializes assignee_id, including null for unassigned items.
	queueItem := registry.Schema(reflect.TypeOf(store.QueueItem{}), true, "RealtimeStoreQueueItem")
	if queueItem.Ref != "" {
		queueItem = registry.SchemaFromRef(queueItem.Ref)
	}
	queueItem.Properties["assignee_id"].Nullable = true
}
func registerBody[I, O any](api huma.API, op huma.Operation, body any, fn func(context.Context, *I) (*O, error)) {
	op.SkipValidateBody = true
	op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "Attendance")}}}
	huma.Register(api, op, fn)
	delete(api.OpenAPI().Paths[op.Path].Post.RequestBody.Content, "application/octet-stream")
}
func decode(data []byte, v any) error {
	if json.Unmarshal(data, v) != nil {
		return huma.Error422UnprocessableEntity("corpo inválido")
	}
	return nil
}
func (h handler) principal(ctx context.Context, c Credentials, write bool) (service.Principal, error) {
	if h.deps.Auth == nil || h.deps.Attendance.DB == nil {
		return service.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		allowed := false
		for _, o := range h.deps.TrustedOrigins {
			if c.Origin != "" && c.Origin == o {
				allowed = true
				break
			}
		}
		if !allowed {
			return service.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, err := h.deps.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if err != nil {
		return service.Principal{}, problem(err)
	}
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return service.Principal{}, huma.Error403Forbidden("acesso negado")
	}
	return service.Principal{UserID: p.UserID, Email: p.Email, MembershipID: p.MembershipID, TenantID: p.TenantID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}, nil
}
func headFrom(v store.Head) head {
	return head{v.ID, v.Protocol, v.Status, v.Version, v.Controller, v.ControllerEpoch, v.AssigneeID, v.ContactID, v.CycleNumber, v.Summary, v.LastCustomerMessageAt, v.WaitingSince, v.CreatedAt}
}
func contentObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func messagesFrom(v []store.Message) []message {
	out := make([]message, 0, len(v))
	for _, m := range v {
		item := message{
			ID: m.ID, ConversationID: m.ConversationID, ExternalMessageID: m.ExternalMessageID,
			Direction: m.Direction, Origin: m.Origin, Visibility: m.Visibility, Body: m.Body,
			LocalState: m.LocalState, TrustedAt: m.TrustedAt, IngestedAt: m.IngestedAt, Historical: m.Historical,
			ContentKind: m.ContentKind, Content: contentObject(m.Content), ReplyToMessageID: m.ReplyToMessageID,
		}
		if m.Attachment != nil {
			item.Attachment = &messageAttachment{ID: m.Attachment.ID, Name: m.Attachment.Name, MediaType: m.Attachment.MediaType, State: m.Attachment.State, Bytes: m.Attachment.Bytes}
		}
		for _, reaction := range m.Reactions {
			item.Reactions = append(item.Reactions, messageReaction{Emoji: reaction.Emoji, ExternalID: reaction.ExternalID, Actor: reaction.Actor})
		}
		out = append(out, item)
	}
	return out
}
func conversationFrom(v store.Conversation) conversation {
	return conversation{v.ID, v.InboxID, v.SectorID, v.ContactID, v.IdentityID, v.Status, v.Controller, v.ControllerEpoch, v.Version, v.AssigneeID, v.Protocol, v.ProtocolPeriod, v.ProtocolCommunicated, v.ExternalProtocol, v.LastCustomerMessageAt, v.LastEligibleHumanReplyAt, v.WaitingSince, v.CycleNumber, v.Summary, v.CreatedAt}
}
func outboundFrom(v store.OutboundResult) outbound {
	return outbound{v.MessageID, v.AttemptID, v.IdempotencyID, v.State, v.Replayed}
}
func (h handler) snapshot(ctx context.Context, in *snapshotInput) (*snapshotOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	id := uuid.Nil
	if in.ConversationID != "" {
		id, e = uuid.Parse(in.ConversationID)
		if e != nil {
			return nil, huma.Error422UnprocessableEntity("identificador inválido")
		}
	}
	v, e := h.deps.Attendance.Snapshot(ctx, p, store.SnapshotInput{InboxID: in.InboxID, ConversationID: id})
	if e != nil {
		return nil, problem(e)
	}
	o := &snapshotOutput{}
	o.Body.NextCursor = v.NextCursor
	o.Body.Conversations = make([]head, 0, len(v.Conversations))
	for _, x := range v.Conversations {
		o.Body.Conversations = append(o.Body.Conversations, headFrom(x))
	}
	o.Body.Messages = messagesFrom(v.Messages)
	return o, nil
}
func (h handler) history(ctx context.Context, in *ConversationInput) (*historyOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.History(ctx, p, in.InboxID, in.ConversationID)
	if e != nil {
		return nil, problem(e)
	}
	o := &historyOutput{}
	o.Body.NextCursor = v.NextCursor
	o.Body.Head = headFrom(v.Head)
	o.Body.Messages = messagesFrom(v.Messages)
	o.Body.Timeline = make([]timeline, 0, len(v.Timeline))
	for _, x := range v.Timeline {
		o.Body.Timeline = append(o.Body.Timeline, timeline{x.ID, x.Kind, x.ActorID, x.At, x.Version, x.ControllerEpoch, x.CycleNumber, x.Controller, x.AssigneeID})
	}
	return o, nil
}
func (h handler) events(ctx context.Context, in *eventsInput) (*eventsOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.Events(ctx, p, in.InboxID, in.After)
	if e != nil {
		return nil, problem(e)
	}
	o := &eventsOutput{}
	o.Body.NextCursor = v.NextCursor
	o.Body.Events = make([]event, 0, len(v.Events))
	visible := map[uuid.UUID]bool{}
	checked := map[uuid.UUID]bool{}
	for _, x := range v.Events {
		if x.AggregateType != "conversation" || x.AggregateID == uuid.Nil {
			continue
		}
		if !checked[x.AggregateID] {
			_, err := h.deps.Attendance.History(ctx, p, in.InboxID, x.AggregateID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, problem(err)
			}
			visible[x.AggregateID] = err == nil
			checked[x.AggregateID] = true
		}
		if visible[x.AggregateID] {
			o.Body.Events = append(o.Body.Events, event{x.Seq, x.ID, x.Type, x.AggregateType, x.AggregateID, x.Version, x.Payload})
		}
	}
	return o, nil
}
func (h handler) assume(ctx context.Context, in *assumeInput) (*conversationOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b versionBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.Assume(ctx, p, in.InboxID, in.ConversationID, b.ExpectedVersion, time.Now().UTC())
	if e != nil {
		return nil, problem(e)
	}
	return &conversationOutput{Body: conversationFrom(v)}, nil
}
func (h handler) send(ctx context.Context, in *sendInput) (*sendOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b sendBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.Send(ctx, p, service.SendInput{InboxID: in.InboxID, ConversationID: in.ConversationID, ExpectedVersion: b.ExpectedVersion, ClientRequestID: b.ClientRequestID, Body: b.Body, At: time.Now().UTC()})
	if e != nil {
		return nil, problem(e)
	}
	return &sendOutput{Body: outboundFrom(v.OutboundResult)}, nil
}
func (h handler) sendTemplate(ctx context.Context, in *templateInput) (*sendOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b templateBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.SendTemplate(ctx, p, service.TemplateSendInput{
		InboxID: in.InboxID, ConversationID: in.ConversationID, ExpectedVersion: b.ExpectedVersion,
		ClientRequestID: b.ClientRequestID, TemplateID: b.TemplateID, Language: b.Language, Purpose: b.Purpose, At: time.Now().UTC(),
	})
	if e != nil {
		return nil, problem(e)
	}
	return &sendOutput{Body: outboundFrom(v.OutboundResult)}, nil
}
func (h handler) close(ctx context.Context, in *closeInput) (*closeOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b closeBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.Close(ctx, p, service.CloseInput{InboxID: in.InboxID, ConversationID: in.ConversationID, ExpectedVersion: b.ExpectedVersion, ClientRequestID: b.ClientRequestID, Reason: b.Reason, Result: b.Result, Summary: b.Summary, FarewellBody: b.FarewellBody, FarewellRequestID: b.FarewellRequestID, At: time.Now().UTC()})
	if e != nil {
		return nil, problem(e)
	}
	o := &closeOutput{}
	o.Body.Conversation = conversationFrom(v.Conversation)
	c := v.Closure
	o.Body.Closure = closure{c.CycleNumber, c.Reason, c.Result, c.Summary, c.AuthorID, c.AuthorKind, c.At, c.ExternalReference}
	o.Body.FarewellFailed = v.FarewellErr != nil
	if v.Farewell != nil {
		x := outboundFrom(v.Farewell.OutboundResult)
		o.Body.Farewell = &x
	}
	return o, nil
}
func (h handler) reopen(ctx context.Context, in *reopenInput) (*conversationOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b reopenBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.Reopen(ctx, p, service.ReopenInput{InboxID: in.InboxID, ConversationID: in.ConversationID, ExpectedVersion: b.ExpectedVersion, ClientRequestID: b.ClientRequestID, At: time.Now().UTC()})
	if e != nil {
		return nil, problem(e)
	}
	return &conversationOutput{Body: conversationFrom(v)}, nil
}
