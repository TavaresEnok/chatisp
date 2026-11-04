package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"chatisp/internal/reports"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type reportInput struct {
	Session  string    `cookie:"__Host-session"`
	From     string    `query:"from" format:"date" doc:"Data inicial, inclusive, no fuso do provedor"`
	To       string    `query:"to" format:"date" doc:"Data final, inclusive, no fuso do provedor"`
	Group    string    `query:"group" enum:"day,week,month" doc:"Agrupamento no fuso do provedor"`
	InboxID  uuid.UUID `query:"inbox_id" doc:"Limita à caixa, quando informado"`
	SectorID uuid.UUID `query:"sector_id" doc:"Limita ao setor, quando informado"`
}

type reportOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         reports.Report
}

func registerReports(api huma.API, deps Dependencies) {
	huma.Register(api, huma.Operation{
		OperationID: "panel-reports-get",
		Method:      http.MethodGet,
		Path:        "/api/panel/reports",
		Summary:     "Relatório do atendimento no período",
		Description: "Calcula tempos e volumes a partir de ciclos, eventos e intervalos persistidos. O CSV usa as mesmas definições da resposta. Não é o histórico completo do provedor.",
		Tags:        []string{"atendimento"},
		Errors:      []int{401, 403, 422, 503},
	}, func(ctx context.Context, in *reportInput) (*reportOutput, error) {
		if deps.Auth == nil || deps.Store == nil {
			return nil, huma.Error503ServiceUnavailable("serviço indisponível")
		}
		principal, err := distributionPrincipal(ctx, deps, in.Session, "", "", false)
		if err != nil {
			return nil, err
		}
		var report reports.Report
		err = deps.Store.WithTenantTx(ctx, principal.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			report, err = reports.Query(ctx, tx, reports.Principal{
				TenantID: principal.TenantID, UserID: principal.UserID, MembershipID: principal.MembershipID, Role: principal.Role,
			}, reports.Filter{FromDate: in.From, ToDate: in.To, Group: in.Group, InboxID: in.InboxID, SectorID: in.SectorID, Now: time.Now().UTC()})
			return err
		})
		if errors.Is(err, reports.ErrForbidden) || errors.Is(err, sectors.ErrForbidden) {
			return nil, huma.Error403Forbidden("acesso negado")
		}
		if errors.Is(err, reports.ErrInvalid) {
			return nil, huma.Error422UnprocessableEntity("período inválido")
		}
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("serviço indisponível")
		}
		return &reportOutput{CacheControl: "no-store", Body: report}, nil
	})
}
