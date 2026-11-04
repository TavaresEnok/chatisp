package httpapi

import (
	"context"
	"errors"
	"net/http"

	"chatisp/internal/attendance/service"
	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type reopenWindowGetInput struct {
	Session string    `cookie:"__Host-session"`
	InboxID uuid.UUID `path:"inbox_id"`
}
type reopenWindowPutInput struct {
	Session string    `cookie:"__Host-session"`
	CSRF    string    `header:"X-CSRF-Token"`
	Origin  string    `header:"Origin"`
	InboxID uuid.UUID `path:"inbox_id"`
	Body    struct {
		Hours int32 `json:"hours" minimum:"0" maximum:"720" doc:"Janela X em horas; 0 desliga a reabertura automática"`
	}
}
type reopenWindowOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		InboxID uuid.UUID `json:"inbox_id"`
		Hours   int32     `json:"hours"`
	}
}

var errReopenInbox = errors.New("caixa não encontrada")

// registerReopenWindow exposes the per-inbox automatic reopen window (M3).
// Admin and supervisor read it; only admin changes it.
func registerReopenWindow(api huma.API, deps Dependencies) {
	path := "/api/panel/inboxes/{inbox_id}/reopen-window"
	op := func(id, method, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"atendimento"}, MaxBodyBytes: 1024, Errors: []int{401, 403, 404, 422, 503}}
	}
	huma.Register(api, op("reopen-window-get", http.MethodGet, "Mostra a janela de reabertura automática da caixa"), func(ctx context.Context, in *reopenWindowGetInput) (*reopenWindowOutput, error) {
		return reopenWindow(ctx, deps, in.Session, "", "", in.InboxID, nil)
	})
	huma.Register(api, op("reopen-window-put", http.MethodPut, "Altera a janela de reabertura automática da caixa"), func(ctx context.Context, in *reopenWindowPutInput) (*reopenWindowOutput, error) {
		if in.Body.Hours < 0 || in.Body.Hours > service.MaxReopenWindowHours {
			return nil, huma.Error422UnprocessableEntity("janela inválida")
		}
		hours := in.Body.Hours
		return reopenWindow(ctx, deps, in.Session, in.CSRF, in.Origin, in.InboxID, &hours)
	})
}

func reopenWindow(ctx context.Context, deps Dependencies, session, csrf, origin string, inbox uuid.UUID, set *int32) (*reopenWindowOutput, error) {
	p, err := distributionPrincipal(ctx, deps, session, csrf, origin, set != nil)
	if err != nil {
		return nil, err
	}
	if p.Role != "admin" && (set != nil || p.Role != "supervisor") {
		return nil, huma.Error403Forbidden("acesso negado")
	}
	out := &reopenWindowOutput{CacheControl: "no-store"}
	out.Body.InboxID = inbox
	err = deps.Store.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
			return err
		}
		if set == nil {
			err := tx.QueryRow(ctx, `SELECT reopen_window_hours FROM inboxes WHERE tenant_id=$1 AND id=$2`, p.TenantID, inbox).Scan(&out.Body.Hours)
			if errors.Is(err, pgx.ErrNoRows) {
				return errReopenInbox
			}
			return err
		}
		var previous int32
		err := tx.QueryRow(ctx, `SELECT reopen_window_hours FROM inboxes WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, p.TenantID, inbox).Scan(&previous)
		if errors.Is(err, pgx.ErrNoRows) {
			return errReopenInbox
		}
		if err != nil {
			return err
		}
		out.Body.Hours = *set
		if previous == *set {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE inboxes SET reopen_window_hours=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, p.TenantID, inbox, *set); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,'inbox.reopen_window','inbox',$5,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, inbox)
		return err
	})
	if errors.Is(err, errReopenInbox) {
		return nil, huma.Error404NotFound("caixa não encontrada")
	}
	if err != nil {
		return nil, distributionProblem(err)
	}
	return out, nil
}
