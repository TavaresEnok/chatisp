package httpapi

import (
	"context"
	"net/http"

	"chatisp/internal/sectors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type occupancyInput struct {
	Session  string    `cookie:"__Host-session"`
	SectorID uuid.UUID `query:"sector_id"`
}
type occupancyMember struct {
	MembershipID uuid.UUID `json:"membership_id"`
	Occupied     int       `json:"occupied" doc:"Conversas que consomem vaga: abertas + aguardando cliente"`
	Capacity     int       `json:"capacity"`
	Open         int       `json:"open" doc:"Abertas atribuídas"`
	Waiting      int       `json:"waiting" doc:"Aguardando cliente atribuídas"`
	Snoozed      int       `json:"snoozed" doc:"Adiadas com retorno preferencial a este atendente; não consomem vaga"`
}
type occupancyOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		Members []occupancyMember `json:"members"`
	}
}

func registerOccupancy(api huma.API, deps Dependencies) {
	huma.Register(api, huma.Operation{OperationID: "presence-occupancy", Method: http.MethodGet, Path: "/api/panel/presence/occupancy", Tags: []string{"presença"}, Errors: []int{401, 403, 503}}, func(ctx context.Context, in *occupancyInput) (*occupancyOutput, error) {
		p, err := distributionPrincipal(ctx, deps, in.Session, "", "", false)
		if err != nil {
			return nil, err
		}
		if in.SectorID != uuid.Nil && p.Role != "admin" && p.Role != "supervisor" {
			return nil, huma.Error403Forbidden("acesso negado")
		}
		out := &occupancyOutput{CacheControl: "no-store"}
		out.Body.Members = []occupancyMember{}
		err = deps.Store.WithTenantTx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `SELECT m.id,COALESCE(ac.capacity,20), (SELECT count(*) FROM conversations c WHERE c.tenant_id=m.tenant_id AND c.assignee_id=m.id AND c.status='open'), (SELECT count(*) FROM conversations c WHERE c.tenant_id=m.tenant_id AND c.assignee_id=m.id AND c.status='waiting_customer'), (SELECT count(*) FROM conversation_snoozes z JOIN conversations c ON c.tenant_id=z.tenant_id AND c.id=z.conversation_id AND c.status<>'closed' WHERE z.tenant_id=m.tenant_id AND z.destination_membership_id=m.id AND z.state='active') FROM memberships m LEFT JOIN assignment_capacities ac ON ac.tenant_id=m.tenant_id AND ac.membership_id=m.id WHERE m.tenant_id=$1 AND m.status='active' AND (($2::uuid IS NULL AND m.id=$3) OR EXISTS(SELECT 1 FROM sector_memberships sm JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active WHERE sm.tenant_id=m.tenant_id AND sm.membership_id=m.id AND sm.sector_id=$2)) ORDER BY m.id`, p.TenantID, nullableUUID(in.SectorID), p.MembershipID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var m occupancyMember
				if err = rows.Scan(&m.MembershipID, &m.Capacity, &m.Open, &m.Waiting, &m.Snoozed); err != nil {
					return err
				}
				m.Occupied = m.Open + m.Waiting
				out.Body.Members = append(out.Body.Members, m)
			}
			return rows.Err()
		})
		if err != nil {
			return nil, distributionProblem(err)
		}
		return out, nil
	})
}
func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
