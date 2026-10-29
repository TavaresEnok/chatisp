package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/google/uuid"

	"chatisp/internal/platform/db"
)

const (
	actionTenantCreate     = "tenant.create"
	actionTenantSuspend    = "tenant.suspend"
	actionTenantReactivate = "tenant.reactivate"
	actionMemberDisable    = "membership.deactivate"
	actionMemberEnable     = "membership.reactivate"
)

// PlanLimits is the V1 plan stored on the provider. A nil field means that limit is unset.
type PlanLimits struct {
	MaxAgents       *int   `json:"max_agents,omitempty"`
	MaxNumbers      *int   `json:"max_numbers,omitempty"`
	MaxStorageBytes *int64 `json:"max_storage_bytes,omitempty"`
}

var (
	ErrNotFound        = errors.New("registro não encontrado")
	ErrPlanLimit       = errors.New("limite de atendentes do plano atingido")
	ErrInvalidTimezone = errors.New("fuso horário inválido")
	ErrInvalidLimits   = errors.New("limites do plano inválidos")
	ErrTenantState     = errors.New("o provedor já está nesse estado")
	ErrMembershipState = errors.New("o vínculo não pode mudar para esse estado")
)

// CreateProvider registers a provider. The caller must be a superadmin with a full session.
// The audit row is platform audit. Attendance tables are not written or read.
func (s *Service) CreateProvider(ctx context.Context, actor Principal, timezone string, limits PlanLimits) (db.Tenant, error) {
	if err := s.requireSuperadmin(actor); err != nil {
		return db.Tenant{}, err
	}
	raw, err := encodeLimits(limits)
	if err != nil {
		return db.Tenant{}, err
	}
	var created db.Tenant
	err = s.store.WithPlatform(ctx, func(ctx context.Context) error {
		row, err := s.store.CreateTenant(ctx, db.NewTenant{
			ID:       uuid.New(),
			Timezone: timezone,
			Status:   "active",
			Limits:   raw,
		})
		if err != nil {
			return err
		}
		created = row
		return s.writePlatformAudit(ctx, actor.UserID, actionTenantCreate, created.ID)
	})
	if err != nil {
		return db.Tenant{}, s.mapProvider(err)
	}
	s.log.Info("provedor criado", "component", "auth")
	return created, nil
}

// SuspendProvider blocks operational access and keeps every stored row.
func (s *Service) SuspendProvider(ctx context.Context, actor Principal, tenantID uuid.UUID) (db.Tenant, error) {
	return s.changeProvider(ctx, actor, tenantID, "suspended", actionTenantSuspend)
}

// ReactivateProvider restores operational access. It does not recreate sessions.
func (s *Service) ReactivateProvider(ctx context.Context, actor Principal, tenantID uuid.UUID) (db.Tenant, error) {
	return s.changeProvider(ctx, actor, tenantID, "active", actionTenantReactivate)
}

// GetProvider returns registry fields of one provider. It has no attendance payload.
func (s *Service) GetProvider(ctx context.Context, actor Principal, tenantID uuid.UUID) (db.Tenant, error) {
	if err := s.requireSuperadmin(actor); err != nil {
		return db.Tenant{}, err
	}
	var row db.Tenant
	err := s.store.WithPlatform(ctx, func(ctx context.Context) error {
		found, err := s.store.GetTenantByID(ctx, tenantID)
		row = found
		return err
	})
	if err != nil {
		return db.Tenant{}, s.mapProvider(err)
	}
	return row, nil
}

// ListProviders returns the provider registry. It does not read attendance.
func (s *Service) ListProviders(ctx context.Context, actor Principal) ([]db.Tenant, error) {
	if err := s.requireSuperadmin(actor); err != nil {
		return nil, err
	}
	var rows []db.Tenant
	err := s.store.WithPlatform(ctx, func(ctx context.Context) error {
		found, err := s.store.ListTenants(ctx)
		rows = found
		return err
	})
	if err != nil {
		s.log.Error("falha ao listar provedores", "component", "auth")
		return nil, ErrUnavailable
	}
	if rows == nil {
		rows = []db.Tenant{}
	}
	return rows, nil
}

// ListMemberships returns the memberships of the actor's provider.
func (s *Service) ListMemberships(ctx context.Context, actor Principal) ([]db.Membership, error) {
	if err := s.requireOperational(ctx, actor); err != nil {
		return nil, err
	}
	if !RoleAllows(actor.Role, ActionMembershipRead) {
		return nil, ErrForbidden
	}
	var rows []db.Membership
	err := s.store.WithTenant(ctx, actor.TenantID, func(ctx context.Context) error {
		found, err := s.store.ListMemberships(ctx)
		rows = found
		return err
	})
	if err != nil {
		s.log.Error("falha ao listar vínculos", "component", "auth")
		return nil, ErrUnavailable
	}
	if rows == nil {
		rows = []db.Membership{}
	}
	return rows, nil
}

// SetMembershipStatus disables or reactivates a membership without deleting it.
// Disabling revokes that user's sessions in the provider. History stays.
func (s *Service) SetMembershipStatus(ctx context.Context, actor Principal, membershipID uuid.UUID, status string) error {
	if err := s.requireOperational(ctx, actor); err != nil {
		return err
	}
	var action Action
	var auditAction string
	switch status {
	case "disabled":
		action = ActionMembershipDisable
		auditAction = actionMemberDisable
	case "active":
		action = ActionMembershipEnable
		auditAction = actionMemberEnable
	default:
		return ErrMembershipState
	}
	if !RoleAllows(actor.Role, action) {
		return ErrForbidden
	}
	if membershipID == uuid.Nil {
		return ErrNotFound
	}
	if status == "disabled" && membershipID == actor.MembershipID {
		return ErrForbidden
	}
	var membership db.Membership
	var changed bool
	err := s.store.WithTenant(ctx, actor.TenantID, func(ctx context.Context) error {
		if err := s.store.LockTenant(ctx); err != nil {
			return err
		}
		tenant, err := s.store.GetTenant(ctx)
		if err != nil {
			return err
		}
		if tenant.Status != "active" {
			return ErrForbidden
		}
		row, err := s.store.FindMembership(ctx, membershipID)
		if err != nil {
			return err
		}
		membership = row
		if row.Status == status {
			return nil
		}
		if status == "active" && row.Status != "disabled" {
			return ErrMembershipState
		}
		if status == "disabled" && row.Status != "active" && row.Status != "invited" {
			return ErrMembershipState
		}
		if status == "active" && row.Role == "agent" {
			if err := s.reserveAgentSeat(ctx, tenant); err != nil {
				return err
			}
		}
		n, err := s.store.UpdateMembershipStatus(ctx, membershipID, status)
		if err != nil {
			return err
		}
		if n != 1 {
			return db.ErrNotFound
		}
		changed = true
		membership.Status = status
		return nil
	})
	if err != nil {
		return s.mapMembership(err)
	}
	if !changed {
		return nil
	}
	if status == "disabled" {
		err = s.store.WithAuth(ctx, func(ctx context.Context) error {
			return s.store.RevokeUserTenantSessions(ctx, actor.TenantID, membership.UserID, s.now())
		})
		if err != nil {
			s.log.Error("falha ao revogar sessões do usuário", "component", "auth")
			return ErrUnavailable
		}
	}
	s.audit(ctx, actor.TenantID, actor.UserID, actor.MembershipID, auditAction, "membership", &membershipID, "success")
	s.log.Info("vínculo atualizado", "component", "auth")
	return nil
}

func (s *Service) changeProvider(ctx context.Context, actor Principal, tenantID uuid.UUID, status, action string) (db.Tenant, error) {
	if err := s.requireSuperadmin(actor); err != nil {
		return db.Tenant{}, err
	}
	if tenantID == uuid.Nil {
		return db.Tenant{}, ErrNotFound
	}
	var updated db.Tenant
	err := s.store.WithPlatform(ctx, func(ctx context.Context) error {
		row, err := s.store.SetTenantStatus(ctx, tenantID, status)
		if err != nil {
			return err
		}
		updated = row
		return s.writePlatformAudit(ctx, actor.UserID, action, tenantID)
	})
	if err != nil {
		return db.Tenant{}, s.mapProvider(err)
	}
	if status == "suspended" {
		err = s.store.WithAuth(ctx, func(ctx context.Context) error {
			return s.store.RevokeTenantSessions(ctx, tenantID, s.now())
		})
		if err != nil {
			s.log.Error("falha ao revogar sessões do provedor", "component", "auth")
			return db.Tenant{}, ErrUnavailable
		}
	}
	s.log.Info("provedor atualizado", "component", "auth")
	return updated, nil
}

func (s *Service) writePlatformAudit(ctx context.Context, actor uuid.UUID, action string, tenantID uuid.UUID) error {
	target := tenantID.String()
	_, err := s.store.CreatePlatformAuditLog(ctx, db.NewPlatformAuditLog{
		Actor:      actor.String(),
		Action:     action,
		TargetType: "tenant",
		TargetID:   &target,
		Result:     "success",
	})
	return err
}

func (s *Service) requireSuperadmin(actor Principal) error {
	if err := s.requirePanel(actor); err != nil {
		return err
	}
	if !actor.IsSuperadmin || actor.UserID == uuid.Nil {
		return ErrForbidden
	}
	return nil
}

func (s *Service) requireOperational(ctx context.Context, actor Principal) error {
	if err := s.requirePanel(actor); err != nil {
		return err
	}
	if actor.TenantID == uuid.Nil {
		return ErrForbidden
	}
	var tenant db.Tenant
	err := s.store.WithPlatform(ctx, func(ctx context.Context) error {
		row, err := s.store.GetTenantByID(ctx, actor.TenantID)
		tenant = row
		return err
	})
	if errors.Is(err, db.ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		s.log.Error("falha ao confirmar provedor", "component", "auth")
		return ErrUnavailable
	}
	if tenant.Status != "active" {
		return ErrForbidden
	}
	return nil
}

func (s *Service) reserveAgentSeat(ctx context.Context, tenant db.Tenant) error {
	limit, limited, err := agentCap(tenant.Limits)
	if err != nil || !limited {
		return err
	}
	n, err := s.store.CountLiveRole(ctx, "agent")
	if err != nil {
		return err
	}
	if n >= limit {
		return ErrPlanLimit
	}
	return nil
}

func (s *Service) mapProvider(err error) error {
	switch {
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, db.ErrTenantUnchanged):
		return ErrTenantState
	case errors.Is(err, db.ErrInvalidTimezone):
		return ErrInvalidTimezone
	case errors.Is(err, db.ErrInvalidLimits), errors.Is(err, ErrInvalidLimits):
		return ErrInvalidLimits
	case errors.Is(err, db.ErrInvalidTenantStatus):
		return ErrTenantState
	default:
		s.log.Error("falha na administração do provedor", "component", "auth")
		return ErrUnavailable
	}
}

func (s *Service) mapMembership(err error) error {
	switch {
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrPlanLimit), errors.Is(err, ErrMembershipState), errors.Is(err, ErrInvalidLimits):
		return err
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, db.ErrInvalidLimits):
		return ErrInvalidLimits
	default:
		s.log.Error("falha ao atualizar vínculo", "component", "auth")
		return ErrUnavailable
	}
}

func encodeLimits(limits PlanLimits) (json.RawMessage, error) {
	if limits.MaxAgents != nil && *limits.MaxAgents < 0 {
		return nil, ErrInvalidLimits
	}
	if limits.MaxNumbers != nil && *limits.MaxNumbers < 0 {
		return nil, ErrInvalidLimits
	}
	if limits.MaxStorageBytes != nil && *limits.MaxStorageBytes < 0 {
		return nil, ErrInvalidLimits
	}
	raw, err := json.Marshal(limits)
	if err != nil {
		return nil, ErrInvalidLimits
	}
	return raw, nil
}

func agentCap(raw json.RawMessage) (int, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}")) {
		return 0, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return 0, false, db.ErrInvalidLimits
	}
	value, ok := object["max_agents"]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return 0, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return 0, false, db.ErrInvalidLimits
	}
	parsed, err := number.Int64()
	if err != nil || parsed < 0 || parsed > math.MaxInt {
		return 0, false, db.ErrInvalidLimits
	}
	return int(parsed), true, nil
}
