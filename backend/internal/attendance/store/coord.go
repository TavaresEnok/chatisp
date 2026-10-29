package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"chatisp/internal/platform/db/dbgen"
)

// AcquireJob reserves one live slot for a queue. Expired rows are removed
// under the slot lock before the count, so two workers cannot both take the last slot.
func (Repo) AcquireJob(ctx context.Context, tx pgx.Tx, tenantID, ownerID uuid.UUID, queue string, expiresAt time.Time) (JobReservation, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return JobReservation{}, err
	}
	maxSlots, err := defaultSlots(queue)
	if err != nil || ownerID == uuid.Nil || expiresAt.IsZero() {
		return JobReservation{}, ErrInvalid
	}
	q := queries(tx)
	if err := q.EnsureJobSlot(ctx, dbgen.EnsureJobSlotParams{
		TenantID:  tenantID,
		QueueName: queue,
		MaxSlots:  maxSlots,
	}); err != nil {
		return JobReservation{}, err
	}
	maxSlots, err = q.LockJobSlot(ctx, dbgen.LockJobSlotParams{TenantID: tenantID, QueueName: queue})
	if err != nil {
		return JobReservation{}, err
	}
	if err := q.DeleteExpiredJobReservations(ctx, dbgen.DeleteExpiredJobReservationsParams{
		TenantID:  tenantID,
		QueueName: queue,
	}); err != nil {
		return JobReservation{}, err
	}
	live, err := q.CountLiveJobReservations(ctx, dbgen.CountLiveJobReservationsParams{
		TenantID:  tenantID,
		QueueName: queue,
	})
	if err != nil {
		return JobReservation{}, err
	}
	if live >= int64(maxSlots) {
		return JobReservation{}, ErrCapacity
	}
	row, err := q.InsertJobReservation(ctx, dbgen.InsertJobReservationParams{
		TenantID:  tenantID,
		ID:        uuid.New(),
		QueueName: queue,
		OwnerID:   ownerID,
		ExpiresAt: ts(expiresAt),
	})
	if err != nil {
		return JobReservation{}, err
	}
	return JobReservation{ID: row.ID, Queue: queue, ExpiresAt: timeOf(row.ExpiresAt)}, nil
}

// ReleaseJob frees one live slot. The slot lock is taken first, same as AcquireJob.
// The owner must match the reservation. A missing row is ErrNotFound.
func (Repo) ReleaseJob(ctx context.Context, tx pgx.Tx, tenantID, reservationID, ownerID uuid.UUID, queue string) error {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	if _, err := defaultSlots(queue); err != nil || reservationID == uuid.Nil || ownerID == uuid.Nil {
		return ErrInvalid
	}
	q := queries(tx)
	if _, err := q.LockJobSlot(ctx, dbgen.LockJobSlotParams{TenantID: tenantID, QueueName: queue}); isNoRow(err) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	n, err := q.DeleteJobReservation(ctx, dbgen.DeleteJobReservationParams{
		TenantID:  tenantID,
		ID:        reservationID,
		QueueName: queue,
		OwnerID:   ownerID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func defaultSlots(queue string) (int32, error) {
	switch queue {
	case QueueIngress:
		return 2, nil
	case QueueInbound:
		return 8, nil
	case QueueOutbound:
		return 4, nil
	default:
		return 0, ErrInvalid
	}
}

// AcquireLease takes or renews the inbox coordinator. A live lease owned by
// somebody else is left untouched.
func (Repo) AcquireLease(ctx context.Context, tx pgx.Tx, tenantID, inboxID, ownerID uuid.UUID, expiresAt time.Time) (Lease, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return Lease{}, err
	}
	if inboxID == uuid.Nil || ownerID == uuid.Nil || expiresAt.IsZero() {
		return Lease{}, ErrInvalid
	}
	row, err := queries(tx).AcquireSendLease(ctx, dbgen.AcquireSendLeaseParams{
		TenantID:  tenantID,
		InboxID:   inboxID,
		OwnerID:   ownerID,
		ExpiresAt: ts(expiresAt),
	})
	if isNoRow(err) {
		return Lease{}, ErrLeaseHeld
	}
	if err != nil {
		return Lease{}, err
	}
	return Lease{OwnerID: row.OwnerID, LeaseVersion: row.LeaseVersion, ExpiresAt: timeOf(row.ExpiresAt)}, nil
}

// RenewLease extends the same version. A stale owner gets ErrLeaseHeld.
func (Repo) RenewLease(ctx context.Context, tx pgx.Tx, tenantID, inboxID, ownerID uuid.UUID, version int64, expiresAt time.Time) (Lease, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return Lease{}, err
	}
	if version < 1 || expiresAt.IsZero() {
		return Lease{}, ErrInvalid
	}
	row, err := queries(tx).RenewSendLease(ctx, dbgen.RenewSendLeaseParams{
		ExpiresAt:    ts(expiresAt),
		TenantID:     tenantID,
		InboxID:      inboxID,
		OwnerID:      ownerID,
		LeaseVersion: version,
	})
	if isNoRow(err) {
		return Lease{}, ErrLeaseHeld
	}
	if err != nil {
		return Lease{}, err
	}
	return Lease{OwnerID: ownerID, LeaseVersion: row.LeaseVersion, ExpiresAt: timeOf(row.ExpiresAt)}, nil
}

// ReserveSend binds one attempt to the current lease version.
func (Repo) ReserveSend(ctx context.Context, tx pgx.Tx, tenantID, inboxID, attemptID, ownerID uuid.UUID, version int64, expiresAt time.Time) (uuid.UUID, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return uuid.Nil, err
	}
	if attemptID == uuid.Nil || version < 1 || expiresAt.IsZero() {
		return uuid.Nil, ErrInvalid
	}
	q := queries(tx)
	if _, err := q.LockSendLease(ctx, dbgen.LockSendLeaseParams{
		TenantID:     tenantID,
		InboxID:      inboxID,
		OwnerID:      ownerID,
		LeaseVersion: version,
	}); isNoRow(err) {
		return uuid.Nil, ErrLeaseHeld
	} else if err != nil {
		return uuid.Nil, err
	}
	id, err := q.InsertSendReservation(ctx, dbgen.InsertSendReservationParams{
		TenantID:     tenantID,
		ID:           uuid.New(),
		InboxID:      inboxID,
		AttemptID:    attemptID,
		OwnerID:      ownerID,
		LeaseVersion: version,
		ExpiresAt:    ts(expiresAt),
	})
	return id, err
}

// ConsumeSend marks a held send reservation consumed. A second call is a no-op.
func (Repo) ConsumeSend(ctx context.Context, tx pgx.Tx, tenantID, reservationID, ownerID uuid.UUID) error {
	return finishSend(ctx, tx, tenantID, reservationID, ownerID, "consumed")
}

// ReleaseSend marks a held send reservation released. A second call is a no-op.
// Consumed and released do not switch into each other.
func (Repo) ReleaseSend(ctx context.Context, tx pgx.Tx, tenantID, reservationID, ownerID uuid.UUID) error {
	return finishSend(ctx, tx, tenantID, reservationID, ownerID, "released")
}

func finishSend(ctx context.Context, tx pgx.Tx, tenantID, reservationID, ownerID uuid.UUID, state string) error {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	if reservationID == uuid.Nil || ownerID == uuid.Nil {
		return ErrInvalid
	}
	q := queries(tx)
	_, err := q.FinishSendReservation(ctx, dbgen.FinishSendReservationParams{
		State:    state,
		TenantID: tenantID,
		ID:       reservationID,
		OwnerID:  ownerID,
	})
	if err == nil {
		return nil
	}
	if !isNoRow(err) {
		return err
	}
	current, err := q.GetSendReservationState(ctx, dbgen.GetSendReservationStateParams{
		TenantID: tenantID,
		ID:       reservationID,
		OwnerID:  ownerID,
	})
	if isNoRow(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current == state {
		return nil
	}
	return ErrInvalid
}

// PutCredential stores ciphertext, key version and AAD. Plaintext is not a column.
func (Repo) PutCredential(ctx context.Context, tx pgx.Tx, tenantID, connectorID uuid.UUID, field string, secret Secret) (Secret, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return Secret{}, err
	}
	field, ok := text(field, 64)
	if !ok || connectorID == uuid.Nil || secret.KeyVersion == "" || len(secret.Ciphertext) == 0 || secret.AAD == "" {
		return Secret{}, ErrInvalid
	}
	row, err := queries(tx).InsertCredential(ctx, dbgen.InsertCredentialParams{
		TenantID:   tenantID,
		ID:         uuid.New(),
		EntityID:   connectorID,
		Field:      field,
		KeyVersion: secret.KeyVersion,
		Ciphertext: secret.Ciphertext,
		Aad:        secret.AAD,
	})
	if err != nil {
		return Secret{}, err
	}
	return Secret{KeyVersion: row.KeyVersion, Ciphertext: append([]byte(nil), secret.Ciphertext...), AAD: row.Aad}, nil
}

// GetCredential reads ciphertext for the active tenant.
func (Repo) GetCredential(ctx context.Context, tx pgx.Tx, tenantID, connectorID uuid.UUID, field string) (Secret, error) {
	if err := requireTenant(ctx, tx, tenantID); err != nil {
		return Secret{}, err
	}
	field, ok := text(field, 64)
	if !ok {
		return Secret{}, ErrInvalid
	}
	row, err := queries(tx).GetCredential(ctx, dbgen.GetCredentialParams{
		TenantID: tenantID,
		EntityID: connectorID,
		Field:    field,
	})
	if isNoRow(err) {
		return Secret{}, ErrNotFound
	}
	if err != nil {
		return Secret{}, err
	}
	return Secret{KeyVersion: row.KeyVersion, Ciphertext: row.Ciphertext, AAD: row.Aad}, nil
}
