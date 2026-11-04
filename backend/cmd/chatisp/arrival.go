package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/platform/db"
	"chatisp/internal/platform/httpapi"
	"chatisp/internal/platform/jobs"
)

// arrivalInbound runs the existing inbound handler and then one distribution
// attempt. The sector flag defaults to off. A replay still calls PickInbound
// so a crash after inbound can finish; the claim makes that call run once.
type arrivalInbound struct {
	next  jobs.InboundHandler
	store *db.Store
	log   *slog.Logger
	now   func() time.Time
}

func (a arrivalInbound) ApplyInbound(ctx context.Context, tenantID, itemID uuid.UUID) error {
	now := time.Now()
	if a.now != nil {
		now = a.now()
	}
	err := httpapi.Arrive(ctx, a.next, a.store, tenantID, itemID, now)
	if err != nil && a.log != nil && errors.Is(err, httpapi.ErrArrivalAssign) {
		a.log.Error("distribuição da chegada falhou", "component", "distribution")
	}
	return err
}
