// Package httpapi exposes media metadata routes. Attachment bytes stay on the
// existing attachment service.
package httpapi

import (
	"context"
	"errors"

	"chatisp/internal/attachments"
	attendance "chatisp/internal/attendance/store"
	"chatisp/internal/media"
	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
	"chatisp/internal/sectors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// Dependencies reuses the panel session and the attachment service already mounted.
type Dependencies struct {
	Auth           *auth.Service
	Store          *db.Store
	Attachments    *attachments.Service
	TrustedOrigins []string
	Media          *media.Service
}

type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type Scope struct {
	Credentials
	InboxID        uuid.UUID `path:"inbox_id"`
	ConversationID uuid.UUID `path:"conversation_id"`
}
type Item struct {
	Scope
	MediaID uuid.UUID `path:"media_id"`
}
type acceptRequest struct {
	Scope
	Body struct {
		AttachmentID     uuid.UUID  `json:"attachment_id"`
		MessageID        *uuid.UUID `json:"message_id,omitempty"`
		ReplyToMessageID *uuid.UUID `json:"reply_to_message_id,omitempty"`
		Voice            bool       `json:"voice,omitempty"`
	}
}
type mediaOutput struct{ Body media.MediaItem }

type handler struct{ d Dependencies }

// Register mounts conversation media routes.
func Register(api huma.API, d Dependencies) {
	if d.Media == nil && d.Store != nil && d.Attachments != nil {
		d.Media = &media.Service{DB: d.Store, Attachments: d.Attachments}
	}
	h := handler{d}
	base := "/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/media"
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"media"}, MaxBodyBytes: 4096, Errors: []int{401, 403, 404, 409, 413, 422, 503}}
	}
	huma.Register(api, op("media-accept", "POST", base), h.accept)
	huma.Register(api, op("media-get", "GET", base+"/{media_id}"), h.get)
	huma.Register(api, op("media-convert", "POST", base+"/{media_id}/convert"), h.convert)
}

func (h handler) principal(ctx context.Context, c Credentials, write bool) (auth.Principal, error) {
	if h.d.Auth == nil || h.d.Media == nil {
		return auth.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
	}
	if write {
		ok := false
		for _, origin := range h.d.TrustedOrigins {
			if origin != "" && c.Origin == origin {
				ok = true
			}
		}
		if !ok {
			return auth.Principal{}, huma.Error403Forbidden("origem não permitida")
		}
	}
	p, e := h.d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
	if e != nil {
		return p, problem(e)
	}
	if p.IsSuperadmin || p.EnrollmentOnly || p.TenantID == uuid.Nil || p.MembershipID == uuid.Nil {
		return p, problem(media.ErrForbidden)
	}
	return p, nil
}

func problem(e error) error {
	switch {
	case errors.Is(e, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(e, auth.ErrCSRF), errors.Is(e, auth.ErrForbidden), errors.Is(e, media.ErrForbidden), errors.Is(e, sectors.ErrForbidden):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(e, attendance.ErrNotFound):
		return huma.Error404NotFound("não encontrado")
	case errors.Is(e, media.ErrFFmpegMissing):
		return huma.Error422UnprocessableEntity(media.FFmpegMissingDetail)
	case errors.Is(e, media.ErrConvert):
		return huma.Error422UnprocessableEntity(media.FFmpegFailedDetail)
	case errors.Is(e, media.ErrInvalid), errors.Is(e, attachments.ErrInvalid):
		return huma.Error422UnprocessableEntity("mídia inválida")
	case errors.Is(e, media.ErrConflict), errors.Is(e, attachments.ErrConflict):
		return huma.Error409Conflict("estado da mídia incompatível")
	case errors.Is(e, attachments.ErrLimit):
		return huma.Error413RequestEntityTooLarge("limite de armazenamento atingido")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}

func (h handler) accept(ctx context.Context, in *acceptRequest) (*mediaOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	item, e := h.d.Media.Accept(ctx, p, in.InboxID, in.ConversationID, media.AcceptInput{
		AttachmentID: in.Body.AttachmentID, MessageID: in.Body.MessageID, ReplyToMessageID: in.Body.ReplyToMessageID, Voice: in.Body.Voice,
	})
	if e != nil {
		return nil, problem(e)
	}
	return &mediaOutput{Body: item}, nil
}

func (h handler) get(ctx context.Context, in *Item) (*mediaOutput, error) {
	p, e := h.principal(ctx, in.Credentials, false)
	if e != nil {
		return nil, e
	}
	item, e := h.d.Media.Get(ctx, p, in.InboxID, in.ConversationID, in.MediaID)
	if e != nil {
		return nil, problem(e)
	}
	return &mediaOutput{Body: item}, nil
}

func (h handler) convert(ctx context.Context, in *Item) (*mediaOutput, error) {
	p, e := h.principal(ctx, in.Credentials, true)
	if e != nil {
		return nil, e
	}
	item, e := h.d.Media.Convert(ctx, p, in.InboxID, in.ConversationID, in.MediaID)
	if e != nil {
		return nil, problem(e)
	}
	return &mediaOutput{Body: item}, nil
}
