package httpapi

import (
	"chatisp/internal/attendance/store"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"net/http"
	"reflect"
)

// RegisterAnnotations adds the optional M3B routes without changing M2 registration.
func RegisterAnnotations(api huma.API, deps Dependencies) {
	h := handler{deps}
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"anotacoes"}, DefaultStatus: 200, MaxBodyBytes: 20000, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	item := "/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/annotations"
	catalog := "/api/panel/annotation-tags"
	huma.Register(api, op("annotation-read", http.MethodGet, item), h.annotationRead)
	huma.Register(api, op("annotation-catalog", http.MethodGet, catalog), h.annotationCatalog)
	registerBody(api, op("annotation-create-tag", http.MethodPost, catalog), annotationTagBody{}, h.annotationCreateTag)
	patch := op("annotation-update-tag", http.MethodPatch, catalog+"/{tag_id}")
	patch.SkipValidateBody = true
	patch.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(annotationTagBody{}), true, "Attendance")}}}
	huma.Register(api, patch, h.annotationUpdateTag)
	delete(api.OpenAPI().Paths[patch.Path].Patch.RequestBody.Content, "application/octet-stream")
	huma.Register(api, op("annotation-delete-tag", http.MethodDelete, catalog+"/{tag_id}"), h.annotationDeleteTag)
	registerBody(api, op("annotation-change", http.MethodPost, item), annotationChangeBody{}, h.annotationChange)
	registerBody(api, op("annotation-mark-read", http.MethodPost, item+"/read"), annotationReadBody{}, h.annotationMarkRead)
}
func (h handler) annotationUpdateTag(ctx context.Context, in *annotationTagInput) (*annotationTagOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b annotationTagBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.ManageAnnotationTag(ctx, p, in.TagID, b.Name, false)
	if e != nil {
		return nil, problem(e)
	}
	return &annotationTagOutput{Body: v}, nil
}
func (h handler) annotationDeleteTag(ctx context.Context, in *annotationTagInput) (*annotationOKOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	_, e = h.deps.Attendance.ManageAnnotationTag(ctx, p, in.TagID, "", true)
	if e != nil {
		return nil, problem(e)
	}
	o := &annotationOKOutput{}
	o.Body.OK = true
	return o, nil
}
func (h handler) annotationCatalog(ctx context.Context, in *Credentials) (*annotationTagsOutput, error) {
	p, e := h.principal(ctx, *in, false)
	if e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.AnnotationCatalog(ctx, p)
	if e != nil {
		return nil, problem(e)
	}
	return &annotationTagsOutput{Body: v}, nil
}
func (h handler) annotationCreateTag(ctx context.Context, in *annotationCatalogInput) (*annotationTagOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b annotationTagBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.CreateAnnotationTag(ctx, p, b.Name)
	if e != nil {
		return nil, problem(e)
	}
	return &annotationTagOutput{Body: v}, nil
}
func (h handler) annotationRead(ctx context.Context, in *ConversationInput) (*annotationStateOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.AnnotationRead(ctx, p, in.InboxID, in.ConversationID)
	if e != nil {
		return nil, problem(e)
	}
	return &annotationStateOutput{Body: v}, nil
}
func (h handler) annotationChange(ctx context.Context, in *annotationInput) (*annotationVersionOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b annotationChangeBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	v, e := h.deps.Attendance.AnnotationChange(ctx, p, in.InboxID, in.ConversationID, b.Kind, b.SubjectID, b.Value, b.RequestID)
	if e != nil {
		return nil, problem(e)
	}
	o := &annotationVersionOutput{}
	o.Body.Version = v
	return o, nil
}
func (h handler) annotationMarkRead(ctx context.Context, in *annotationInput) (*annotationOKOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	var b annotationReadBody
	if e = decode(in.RawBody, &b); e != nil {
		return nil, e
	}
	if b.ReadVersion == nil {
		return nil, problem(store.ErrInvalid)
	}
	if e = h.deps.Attendance.AnnotationMarkRead(ctx, p, in.InboxID, in.ConversationID, b.Watermark, b.Unread, *b.ReadVersion); e != nil {
		return nil, problem(e)
	}
	o := &annotationOKOutput{}
	o.Body.OK = true
	return o, nil
}
