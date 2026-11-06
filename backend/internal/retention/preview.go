package retention

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// PurgePreview describes a bounded, read-only census. Counts describe only the
// named scope, never permission to delete. Execution remains unavailable until
// class semantics and recovery of deletions are complete.
type PurgePreview struct {
	AsOf               time.Time           `json:"as_of"`
	AutomaticPurge     bool                `json:"automatic_purge"`
	ExecutionAvailable bool                `json:"execution_available"`
	Classes            []PurgeClassPreview `json:"classes"`
}

type PurgeClassPreview struct {
	Class      string     `json:"class"`
	Days       *int       `json:"days"`
	Cutoff     *time.Time `json:"cutoff,omitempty"`
	Candidates *int       `json:"candidates,omitempty"`
	Capped     bool       `json:"capped"`
	Scope      string     `json:"scope"`
	Blocker    string     `json:"blocker"`
}

// PreviewPurge uses the provider's saved durations; it invents neither legal
// periods nor an eligibility rule for operational/audit data. For content and
// media it counts a conservative subset: old data in closed, inactive
// conversations. The caller chooses a technical count bound (1..10000).
func (s Service) PreviewPurge(ctx context.Context, p Principal, limit int) (PurgePreview, error) {
	if limit < 1 || limit > 10000 {
		return PurgePreview{}, ErrInvalid
	}
	out := PurgePreview{Classes: []PurgeClassPreview{}}
	err := s.read(ctx, p, true, func(ctx context.Context, tx pgx.Tx) error {
		profile, err := loadPolicy(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&out.AsOf); err != nil {
			return err
		}
		days := []*int{profile.ContentDays, profile.MediaDays, profile.EnvelopeDays, profile.LogDays, profile.JobDays}
		for i, class := range Classes {
			item := PurgeClassPreview{Class: class, Days: days[i], Scope: "sem seleção executável", Blocker: "Falta definir os dados elegíveis e preservados desta classe; executor não implementado."}
			if days[i] == nil {
				item.Blocker = "Prazo da classe não definido pelo provedor; nenhuma seleção autorizada."
				out.Classes = append(out.Classes, item)
				continue
			}
			cutoff := out.AsOf.AddDate(0, 0, -*days[i])
			item.Cutoff = &cutoff
			var query string
			switch class {
			case "content":
				item.Scope = "corpos de mensagens em conversas encerradas e sem atividade antes do corte"
				item.Blocker = "Executor não implementado: definir conteúdo preservado e reaplicar redações após restore antes de habilitar."
				query = `SELECT count(*) FROM (SELECT 1 FROM messages m JOIN conversations c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id WHERE m.tenant_id=$1 AND m.ingested_at<$2 AND m.body<>'' AND c.status='closed' AND c.updated_at<$2 LIMIT $3) candidates`
			case "media":
				item.Scope = "anexos validados em conversas encerradas e sem atividade antes do corte"
				item.Blocker = "Executor não implementado: definir exceções de preservação, remoção recuperável de objetos e livro de exclusões."
				query = `SELECT count(*) FROM (SELECT 1 FROM attachments a JOIN conversations c ON c.tenant_id=a.tenant_id AND c.id=a.conversation_id WHERE a.tenant_id=$1 AND a.created_at<$2 AND a.state='validated' AND c.status='closed' AND c.updated_at<$2 LIMIT $3) candidates`
			case "envelope":
				item.Blocker = "Definir envelope bruto versus evidência/quarentena, finalização e vínculo por tenant antes de implementar remoção no ingresso."
			case "log":
				item.Blocker = "Definir logs operacionais versus auditoria preservada e destino externo antes de implementar remoção."
			case "job":
				item.Blocker = "Definir jobs finalizados, incertos e evidência de idempotência antes de implementar remoção nas filas River."
			}
			if query != "" {
				var n int
				if err := tx.QueryRow(ctx, query, p.TenantID, cutoff, limit+1).Scan(&n); err != nil {
					return err
				}
				if n > limit {
					n = limit
					item.Capped = true
				}
				item.Candidates = &n
			}
			out.Classes = append(out.Classes, item)
		}
		return nil
	})
	return out, err
}
