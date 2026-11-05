package retention

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// MetaUsageNote is shown with the aggregate. It states what the numbers are not.
const MetaUsageNote = "Contagem do que a Meta informou nos status de entrega: categoria e indicador de cobrança. Não há valor em dinheiro, não é fatura e não substitui o painel da Meta."

// MetaUsageRow is one (category, billable) pair and how many distinct messages carry it.
type MetaUsageRow struct {
	Category string `json:"category"`
	Billable string `json:"billable"` // "true", "false" or "unknown"
	Messages int    `json:"messages"`
}

// MetaUsage is rebuilt from message_status_events on every read, so it cannot drift
// from the stored events. A message counts once, with the latest status that carried pricing.
type MetaUsage struct {
	From           string         `json:"from"`
	To             string         `json:"to"`
	Rows           []MetaUsageRow `json:"rows"`
	WithoutPricing int            `json:"without_pricing"`
	Note           string         `json:"note"`
}

// MetaUsage aggregates the pricing the Meta reported inside the civil window of the provider timezone.
func (s Service) MetaUsage(ctx context.Context, p Principal, fromDate, toDate string) (MetaUsage, error) {
	out := MetaUsage{Rows: []MetaUsageRow{}, Note: MetaUsageNote}
	if p.Role == "agent" {
		return out, ErrForbidden
	}
	err := s.read(ctx, p, false, func(ctx context.Context, tx pgx.Tx) error {
		var zone string
		if err := tx.QueryRow(ctx, `SELECT timezone FROM tenants WHERE id = $1`, p.TenantID).Scan(&zone); err != nil {
			return err
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return ErrInvalid
		}
		from, err := time.ParseInLocation("2006-01-02", fromDate, loc)
		if err != nil {
			return ErrInvalid
		}
		last, err := time.ParseInLocation("2006-01-02", toDate, loc)
		if err != nil || last.Before(from) || last.Sub(from) > 366*24*time.Hour {
			return ErrInvalid
		}
		end := last.AddDate(0, 0, 1)
		out.From, out.To = fromDate, toDate
		rows, err := tx.Query(ctx, `
			WITH latest AS (
				SELECT DISTINCT ON (k) k, billing_category, billable,
				       (billing_category IS NOT NULL OR billable <> 'unknown') AS priced
				FROM (
					SELECT coalesce(message_id::text, external_message_id, dedup_key) AS k,
					       billing_category, billable, ingested_at
					FROM message_status_events
					WHERE tenant_id = $1 AND ingested_at >= $2 AND ingested_at < $3
				) e
				ORDER BY k, (billing_category IS NOT NULL OR billable <> 'unknown') DESC, ingested_at DESC
			)
			SELECT priced, coalesce(billing_category, 'sem categoria'), billable, count(*)::int
			FROM latest
			GROUP BY 1, 2, 3
			ORDER BY 2, 3
		`, p.TenantID, from, end)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var priced bool
			var row MetaUsageRow
			if err := rows.Scan(&priced, &row.Category, &row.Billable, &row.Messages); err != nil {
				return err
			}
			if priced {
				out.Rows = append(out.Rows, row)
			} else {
				out.WithoutPricing += row.Messages
			}
		}
		return rows.Err()
	})
	return out, err
}
