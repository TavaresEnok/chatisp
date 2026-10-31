package cannedreplies

import (
	"context"
	"errors"
	"net/http"

	attendanceStore "chatisp/internal/attendance/store"
	"chatisp/internal/platform/auth"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

type Dependencies struct {
	Auth           *auth.Service
	CannedReplies  Service
	TrustedOrigins []string
}
type Credentials struct {
	Session string `cookie:"__Host-session"`
	CSRF    string `header:"X-CSRF-Token"`
	Origin  string `header:"Origin"`
}
type listInput struct {
	Credentials
	Search string `query:"search" maxLength:"120"`
	Limit  int    `query:"limit" default:"25" minimum:"1" maximum:"100"`
	Offset int    `query:"offset" default:"0" minimum:"0" maximum:"100000"`
}
type createInput struct {
	Credentials
	Body Edit
}
type updateInput struct {
	Credentials
	ReplyID uuid.UUID `path:"reply_id"`
	Body    struct {
		Edit
		Version int64 `json:"version" minimum:"1"`
	}
}
type deleteInput struct {
	Credentials
	ReplyID uuid.UUID `path:"reply_id"`
	Version int64     `query:"version" minimum:"1" required:"true"`
}
type renderInput struct {
	Credentials
	ReplyID uuid.UUID `path:"reply_id"`
	Body    struct {
		InboxID        uuid.UUID `json:"inbox_id"`
		ConversationID uuid.UUID `json:"conversation_id"`
	}
}
type signatureInput struct {
	Credentials
	Body Signature
}
type output[T any] struct{ Body T }
type empty struct{}

// Register declares the modular HTTP contract; runtime composition is deferred.
func Register(api huma.API, d Dependencies) {
	authorize := func(ctx context.Context, c Credentials, write bool) (auth.Principal, error) {
		if d.Auth == nil || d.CannedReplies.DB == nil {
			return auth.Principal{}, huma.Error503ServiceUnavailable("serviço indisponível")
		}
		if write {
			allowed := false
			for _, origin := range d.TrustedOrigins {
				if c.Origin != "" && origin == c.Origin {
					allowed = true
					break
				}
			}
			if !allowed {
				return auth.Principal{}, huma.Error403Forbidden("origem não permitida")
			}
		}
		p, err := d.Auth.Authenticate(ctx, c.Session, c.CSRF, write)
		if err != nil {
			return p, problem(err)
		}
		if p.IsSuperadmin || p.EnrollmentOnly {
			return p, problem(ErrForbidden)
		}
		return p, nil
	}
	op := func(id, method, path string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, DefaultStatus: status, Tags: []string{"respostas-prontas"}, MaxBodyBytes: 65536, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	base := "/api/panel/canned-replies"
	item := base + "/{reply_id}"
	huma.Register(api, op("canned-replies-list", http.MethodGet, base, 200), func(ctx context.Context, in *listInput) (*output[Page], error) {
		p, err := authorize(ctx, in.Credentials, false)
		if err != nil {
			return nil, err
		}
		v, err := d.CannedReplies.List(ctx, p, in.Search, in.Limit, in.Offset)
		if err != nil {
			return nil, problem(err)
		}
		return &output[Page]{Body: v}, nil
	})
	huma.Register(api, op("canned-replies-create", http.MethodPost, base, 201), func(ctx context.Context, in *createInput) (*output[Reply], error) {
		p, err := authorize(ctx, in.Credentials, true)
		if err != nil {
			return nil, err
		}
		v, err := d.CannedReplies.Create(ctx, p, in.Body)
		if err != nil {
			return nil, problem(err)
		}
		return &output[Reply]{Body: v}, nil
	})
	huma.Register(api, op("canned-replies-update", http.MethodPut, item, 200), func(ctx context.Context, in *updateInput) (*output[Reply], error) {
		p, err := authorize(ctx, in.Credentials, true)
		if err != nil {
			return nil, err
		}
		v, err := d.CannedReplies.Update(ctx, p, in.ReplyID, in.Body.Version, in.Body.Edit)
		if err != nil {
			return nil, problem(err)
		}
		return &output[Reply]{Body: v}, nil
	})
	huma.Register(api, op("canned-replies-delete", http.MethodDelete, item, 200), func(ctx context.Context, in *deleteInput) (*output[empty], error) {
		p, err := authorize(ctx, in.Credentials, true)
		if err != nil {
			return nil, err
		}
		if err = d.CannedReplies.Delete(ctx, p, in.ReplyID, in.Version); err != nil {
			return nil, problem(err)
		}
		return &output[empty]{}, nil
	})
	huma.Register(api, op("canned-replies-render", http.MethodPost, item+"/render", 200), func(ctx context.Context, in *renderInput) (*output[Draft], error) {
		p, err := authorize(ctx, in.Credentials, true)
		if err != nil {
			return nil, err
		}
		v, err := d.CannedReplies.Render(ctx, p, in.ReplyID, in.Body.InboxID, in.Body.ConversationID)
		if err != nil {
			return nil, problem(err)
		}
		return &output[Draft]{Body: v}, nil
	})
	path := "/api/panel/attendant-signature"
	huma.Register(api, op("attendant-signature-get", http.MethodGet, path, 200), func(ctx context.Context, in *Credentials) (*output[Signature], error) {
		p, err := authorize(ctx, *in, false)
		if err != nil {
			return nil, err
		}
		v, err := d.CannedReplies.Signature(ctx, p)
		if err != nil {
			return nil, problem(err)
		}
		return &output[Signature]{Body: v}, nil
	})
	huma.Register(api, op("attendant-signature-update", http.MethodPut, path, 200), func(ctx context.Context, in *signatureInput) (*output[Signature], error) {
		p, err := authorize(ctx, in.Credentials, true)
		if err != nil {
			return nil, err
		}
		v, err := d.CannedReplies.SetSignature(ctx, p, in.Body)
		if err != nil {
			return nil, problem(err)
		}
		return &output[Signature]{Body: v}, nil
	})
}
func problem(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return huma.Error401Unauthorized("não autenticado")
	case errors.Is(err, ErrForbidden), errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrEnrollmentRequired):
		return huma.Error403Forbidden("acesso negado")
	case errors.Is(err, ErrInvalid):
		return huma.Error422UnprocessableEntity("comando inválido")
	case errors.Is(err, ErrNotFound), errors.Is(err, attendanceStore.ErrNotFound):
		return huma.Error404NotFound("resposta não encontrada")
	case errors.Is(err, ErrConflict):
		return huma.Error409Conflict("conflito")
	default:
		return huma.Error503ServiceUnavailable("serviço indisponível")
	}
}
