package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"chatisp/internal/attendance/store"
	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AssignmentInput struct {
	InboxID, ConversationID, TargetID uuid.UUID
	ExpectedVersion                   int64
	ClientRequestID                   string
	At                                time.Time
}

func (s Service) TransferToMember(ctx context.Context, p Principal, in AssignmentInput) (store.Conversation, error) {
	return s.assign(ctx, p, in, "transferred_member")
}
func (s Service) TransferToSector(ctx context.Context, p Principal, in AssignmentInput) (store.Conversation, error) {
	return s.assign(ctx, p, in, "transferred_sector")
}
func (s Service) Unassign(ctx context.Context, p Principal, in AssignmentInput) (store.Conversation, error) {
	return s.assign(ctx, p, in, "unassigned")
}

func (s Service) assign(ctx context.Context, p Principal, in AssignmentInput, kind string) (store.Conversation, error) {
	var out store.Conversation
	in.ClientRequestID = strings.TrimSpace(in.ClientRequestID)
	if in.ExpectedVersion < 1 || in.At.IsZero() || in.ClientRequestID == "" || len(in.ClientRequestID) > 200 || (kind != "unassigned" && in.TargetID == uuid.Nil) || (kind == "unassigned" && in.TargetID != uuid.Nil) {
		return out, store.ErrInvalid
	}
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, in.InboxID, false); err != nil {
			return err
		}
		conv, err := lockAuthorizedConversation(ctx, tx, p, in.InboxID, in.ConversationID)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(struct {
			Kind    string
			Target  uuid.UUID
			Version int64
		}{kind, in.TargetID, in.ExpectedVersion})
		if err != nil {
			return err
		}
		var previousPayload, previousResult []byte
		err = tx.QueryRow(ctx, `SELECT payload,result FROM assignment_requests WHERE tenant_id=$1 AND conversation_id=$2 AND principal_id=$3 AND client_request_id=$4`, p.TenantID, in.ConversationID, p.MembershipID, in.ClientRequestID).Scan(&previousPayload, &previousResult)
		if err == nil {
			var a, b any
			if json.Unmarshal(payload, &a) != nil || json.Unmarshal(previousPayload, &b) != nil {
				return store.ErrInvalid
			}
			x, _ := json.Marshal(a)
			y, _ := json.Marshal(b)
			if string(x) != string(y) {
				return store.ErrIdempotencyConflict
			}
			return json.Unmarshal(previousResult, &out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if conv.Version != in.ExpectedVersion {
			return store.ErrStaleVersion
		}
		if conv.Status == store.StatusClosed {
			return store.ErrClosed
		}
		var sector uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT sector_id FROM conversations WHERE tenant_id=$1 AND id=$2`, p.TenantID, in.ConversationID).Scan(&sector); err != nil {
			return err
		}
		var member, targetSector uuid.UUID
		if kind == "transferred_member" {
			var id uuid.UUID
			err = tx.QueryRow(ctx, `SELECT m.id FROM memberships m JOIN sector_memberships sm ON sm.tenant_id=m.tenant_id AND sm.membership_id=m.id JOIN sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active JOIN sector_inboxes si ON si.tenant_id=s.tenant_id AND si.sector_id=s.id WHERE m.tenant_id=$1 AND m.id=$2 AND m.status='active' AND m.role IN ('admin','supervisor','agent') AND s.id=$3 AND si.inbox_id=$4 FOR SHARE OF m,sm,s,si`, p.TenantID, in.TargetID, sector, in.InboxID).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrForbidden
			}
			if err != nil {
				return err
			}
			if err = store.LockAssignmentCapacity(ctx, tx, p.TenantID, in.TargetID, in.ConversationID); err != nil {
				return err
			}
			member = in.TargetID
		} else if kind == "transferred_sector" {
			var id uuid.UUID
			err = tx.QueryRow(ctx, `SELECT s.id FROM sectors s JOIN sector_inboxes si ON si.tenant_id=s.tenant_id AND si.sector_id=s.id WHERE s.tenant_id=$1 AND s.id=$2 AND s.active AND si.inbox_id=$3 FOR SHARE OF s,si`, p.TenantID, in.TargetID, in.InboxID).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrForbidden
			}
			if err != nil {
				return err
			}
			targetSector = id
		}
		resolution := "queued"
		if member != uuid.Nil {
			resolution = "assigned"
		}
		cleared, err := clearManualSnooze(ctx, tx, p.TenantID, in.ConversationID, in.At, resolution)
		if err != nil {
			return err
		}
		out, err = s.Repo.ApplyAssignment(ctx, tx, p.TenantID, in.ConversationID, p.UserID, member, targetSector, in.ExpectedVersion, kind, in.At)
		if err != nil {
			return err
		}
		if err := recordManualSnoozeClear(ctx, tx, p.TenantID, p.UserID, out, in.At, cleared); err != nil {
			return err
		}
		result, err := json.Marshal(out)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO assignment_requests(tenant_id,conversation_id,principal_id,client_request_id,payload,result) VALUES($1,$2,$3,$4,$5,$6)`, p.TenantID, in.ConversationID, p.MembershipID, in.ClientRequestID, payload, result)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result,correlation_id) VALUES($1,$2,$3,$4,$5,'conversation',$6,'success',$7)`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, kind, in.ConversationID, in.ClientRequestID)
		return err
	})
	return out, err
}

type AssignmentCandidate struct {
	MembershipID uuid.UUID `json:"membership_id"`
	Capacity     int       `json:"capacity"`
	Assigned     int       `json:"assigned"`
}

func (s Service) AssignmentCandidates(ctx context.Context, p Principal, inbox, conversation uuid.UUID) ([]AssignmentCandidate, error) {
	out := []AssignmentCandidate{}
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inbox, false); err != nil {
			return err
		}
		if err := requireConversation(ctx, tx, p, inbox, conversation); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT m.id,COALESCE(ac.capacity,20),(SELECT count(*) FROM conversations x WHERE x.tenant_id=m.tenant_id AND x.assignee_id=m.id AND x.status IN ('open','waiting_customer')) FROM conversations c JOIN sector_memberships sm ON sm.tenant_id=c.tenant_id AND sm.sector_id=c.sector_id JOIN memberships m ON m.tenant_id=sm.tenant_id AND m.id=sm.membership_id AND m.status='active' AND m.role IN ('admin','supervisor','agent') JOIN sectors s ON s.tenant_id=c.tenant_id AND s.id=c.sector_id AND s.active JOIN sector_inboxes si ON si.tenant_id=c.tenant_id AND si.sector_id=c.sector_id AND si.inbox_id=c.inbox_id LEFT JOIN assignment_capacities ac ON ac.tenant_id=m.tenant_id AND ac.membership_id=m.id WHERE c.tenant_id=$1 AND c.id=$2 AND c.inbox_id=$3 ORDER BY m.id`, p.TenantID, conversation, inbox)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c AssignmentCandidate
			if err := rows.Scan(&c.MembershipID, &c.Capacity, &c.Assigned); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

func (s Service) SetAssignmentCapacity(ctx context.Context, p Principal, member uuid.UUID, capacity int) error {
	if member == uuid.Nil || capacity < 1 {
		return store.ErrInvalid
	}
	return s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorizePrincipal(ctx, tx, p); err != nil {
			return err
		}
		if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); err != nil {
			if errors.Is(err, sectors.ErrForbidden) {
				return ErrForbidden
			}
			return err
		}
		var role string
		if err := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE tenant_id=$1 AND id=$2 AND user_id=$3 AND status='active' FOR SHARE`, p.TenantID, p.MembershipID, p.UserID).Scan(&role); err != nil {
			return err
		}
		if role != "admin" && role != "supervisor" {
			return ErrForbidden
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM memberships WHERE tenant_id=$1 AND id=$2 AND status='active' FOR SHARE`, p.TenantID, member).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('chatisp-assignment-capacity:' || $1::text || ':' || $2::text, 0))`, p.TenantID, member); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO assignment_capacities(tenant_id,membership_id,capacity) VALUES($1,$2,$3) ON CONFLICT(tenant_id,membership_id) DO UPDATE SET capacity=excluded.capacity`, p.TenantID, member, capacity); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,tenant_id,membership_id,actor_user_id,action,target_type,target_id,result) VALUES($1,$2,$3,$4,'assignment_capacity_changed','membership',$5,'success')`, uuid.New(), p.TenantID, p.MembershipID, p.UserID, member)
		return err
	})
}
