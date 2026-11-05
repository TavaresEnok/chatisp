package auth

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"chatisp/internal/platform/db"
)

func (s *Service) loginLocked(ctx context.Context, email, ip string) (bool, error) {
	var locked bool
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		for _, item := range []struct {
			kind  string
			value string
		}{
			{kindLoginAccount, email},
			{kindLoginIP, normalizeIP(ip)},
		} {
			until, err := s.store.LockedUntil(ctx, item.kind, SubjectHash(item.kind, item.value), s.now())
			if err != nil {
				return err
			}
			if !until.IsZero() && until.After(s.now()) {
				locked = true
			}
		}
		return nil
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao consultar bloqueio", "component", "auth")
		return false, ErrUnavailable
	}
	return locked, nil
}

func (s *Service) loadIdentity(ctx context.Context, email string) (db.LoginIdentity, bool, error) {
	var identity db.LoginIdentity
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		identity, err = s.store.LoginIdentity(ctx, email)
		return err
	})
	if errors.Is(err, db.ErrNotFound) {
		return db.LoginIdentity{}, false, nil
	}
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao ler identidade", "component", "auth")
		return db.LoginIdentity{}, false, ErrUnavailable
	}
	return identity, true, nil
}

func (s *Service) recordLoginFailure(ctx context.Context, email, ip string) error {
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		if err := s.bumpLock(ctx, kindLoginAccount, email, s.policy.LockoutThreshold); err != nil {
			return err
		}
		return s.bumpLock(ctx, kindLoginIP, normalizeIP(ip), s.policy.LockoutThreshold)
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao registrar tentativa", "component", "auth")
		return ErrUnavailable
	}
	return nil
}

func (s *Service) bumpLock(ctx context.Context, kind, value string, limit int) error {
	attempt, err := s.store.BumpFailure(ctx, kind, SubjectHash(kind, value), s.now(), s.policy.LockoutWindow)
	if err != nil {
		return err
	}
	if attempt.AlreadyLocked || attempt.Failures < limit {
		return nil
	}
	until := s.now().Add(s.lockFor(attempt.Failures))
	return s.store.SetLock(ctx, kind, SubjectHash(kind, value), until)
}

func (s *Service) lockFor(failures int) time.Duration {
	steps := failures - s.policy.LockoutThreshold
	if steps < 0 {
		steps = 0
	}
	if steps > 16 {
		steps = 16
	}
	duration := s.policy.LockoutBase << steps
	if duration <= 0 || duration > s.policy.LockoutMax {
		return s.policy.LockoutMax
	}
	return duration
}

func (s *Service) auditLoginFailure(ctx context.Context, identity db.LoginIdentity, found bool) {
	if !found {
		return
	}
	membership, ok := firstActive(identity.Memberships)
	if !ok {
		return
	}
	s.audit(ctx, membership.TenantID, identity.UserID, membership.MembershipID, "login", "session", nil, "denied")
}

func (s *Service) audit(ctx context.Context, tenantID, actor, membershipID uuid.UUID, action, targetType string, targetID *uuid.UUID, result string) {
	err := s.store.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		_, err := s.store.CreateAuditLog(ctx, db.NewAuditLog{
			MembershipID: &membershipID,
			ActorUserID:  &actor,
			Action:       action,
			TargetType:   targetType,
			TargetID:     targetID,
			Result:       result,
		})
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao auditar", "component", "auth")
	}
}

func (s *Service) auditPasswordReset(ctx context.Context, userID uuid.UUID) {
	var email string
	err := s.store.WithPlatform(ctx, func(ctx context.Context) error {
		user, err := s.store.FindUserByID(ctx, userID)
		if err != nil {
			return err
		}
		email = user.Email
		return nil
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao auditar", "component", "auth")
		return
	}
	identity, found, err := s.loadIdentity(ctx, email)
	if err != nil || !found {
		return
	}
	membership, ok := firstActive(identity.Memberships)
	if !ok {
		return
	}
	s.audit(ctx, membership.TenantID, identity.UserID, membership.MembershipID, "password_reset.confirm", "user", &identity.UserID, "success")
}

func chooseMembership(rows []db.LoginMembership, tenantRaw string) (db.LoginMembership, error) {
	active := make([]db.LoginMembership, 0, len(rows))
	for _, row := range rows {
		if row.Status == "active" && row.TenantStatus == "active" {
			active = append(active, row)
		}
	}
	selected := strings.TrimSpace(tenantRaw)
	if selected == "" {
		switch len(active) {
		case 0:
			return db.LoginMembership{}, ErrForbidden
		case 1:
			return active[0], nil
		default:
			return db.LoginMembership{}, ErrTenantRequired
		}
	}
	tenantID, err := uuid.Parse(selected)
	if err != nil {
		return db.LoginMembership{}, ErrTenantRequired
	}
	for _, row := range active {
		if row.TenantID == tenantID {
			return row, nil
		}
	}
	return db.LoginMembership{}, ErrForbidden
}

func (s *Service) ensureUser(ctx context.Context, email string) (db.User, error) {
	var user db.User
	err := s.store.WithPlatform(ctx, func(ctx context.Context) error {
		found, err := s.store.FindUserByEmail(ctx, email)
		if err == nil {
			user = found
			return nil
		}
		if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		created, err := s.store.CreateUser(ctx, db.NewUser{Email: email, Status: "invited"})
		if err != nil {
			return err
		}
		user = created
		return nil
	})
	if isUnique(err) {
		err = s.store.WithPlatform(ctx, func(ctx context.Context) error {
			found, err := s.store.FindUserByEmail(ctx, email)
			if err != nil {
				return err
			}
			user = found
			return nil
		})
	}
	if err != nil {
		if errors.Is(err, db.ErrInvalidEmail) {
			return db.User{}, ErrInvalidEmail
		}
		s.log.ErrorContext(ctx, "falha ao preparar usuário", "component", "auth")
		return db.User{}, ErrUnavailable
	}
	return user, nil
}

func (s *Service) ensureInvitedMembership(ctx context.Context, tenantID, userID uuid.UUID, role string) (db.Membership, error) {
	var membership db.Membership
	err := s.store.WithTenant(ctx, tenantID, func(ctx context.Context) error {
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
		found, err := s.store.FindMembershipByUser(ctx, userID)
		if err == nil {
			membership = found
			return nil
		}
		if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if role == "agent" {
			if err := s.reserveAgentSeat(ctx, tenant); err != nil {
				return err
			}
		}
		created, err := s.store.CreateMembership(ctx, db.NewMembership{
			UserID: userID,
			Role:   role,
			Status: "invited",
		})
		if err != nil {
			return err
		}
		membership = created
		return nil
	})
	if isUnique(err) {
		err = s.store.WithTenant(ctx, tenantID, func(ctx context.Context) error {
			found, err := s.store.FindMembershipByUser(ctx, userID)
			if err != nil {
				return err
			}
			membership = found
			return nil
		})
	}
	if err != nil {
		if errors.Is(err, ErrPlanLimit) || errors.Is(err, ErrInvalidLimits) || errors.Is(err, ErrForbidden) || errors.Is(err, db.ErrInvalidLimits) {
			if errors.Is(err, db.ErrInvalidLimits) {
				return db.Membership{}, ErrInvalidLimits
			}
			return db.Membership{}, err
		}
		s.log.ErrorContext(ctx, "falha ao preparar convite", "component", "auth")
		return db.Membership{}, ErrUnavailable
	}
	switch membership.Status {
	case "active":
		return db.Membership{}, ErrConflict
	case "disabled":
		return db.Membership{}, ErrForbidden
	case "invited":
		return membership, nil
	default:
		return db.Membership{}, ErrForbidden
	}
}

func (s *Service) guardTokenIP(ctx context.Context, ip string) error {
	var until time.Time
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		until, err = s.store.LockedUntil(ctx, kindTokenIP, SubjectHash(kindTokenIP, normalizeIP(ip)), s.now())
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao consultar bloqueio", "component", "auth")
		return ErrUnavailable
	}
	if !until.IsZero() && until.After(s.now()) {
		return ErrLocked
	}
	return nil
}

func (s *Service) noteTokenFailure(ctx context.Context, ip string) {
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		return s.bumpLock(ctx, kindTokenIP, normalizeIP(ip), s.policy.TokenFailureLimit)
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao registrar tentativa", "component", "auth")
	}
}

func (s *Service) clearKind(ctx context.Context, kind, value string) error {
	if kind == kindLoginIP || kind == kindResetIP || kind == kindTokenIP {
		value = normalizeIP(value)
	}
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		return s.store.ClearFailures(ctx, kind, SubjectHash(kind, value))
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao limpar tentativas", "component", "auth")
		return ErrUnavailable
	}
	return nil
}

func (s *Service) resetSuppressed(ctx context.Context, email, ip string) (bool, error) {
	var suppressed bool
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		account, err := s.store.BumpFailure(ctx, kindResetAccount, SubjectHash(kindResetAccount, email), s.now(), s.policy.LockoutWindow)
		if err != nil {
			return err
		}
		remote, err := s.store.BumpFailure(ctx, kindResetIP, SubjectHash(kindResetIP, normalizeIP(ip)), s.now(), s.policy.LockoutWindow)
		if err != nil {
			return err
		}
		suppressed = account.Failures > s.policy.ResetLimit || remote.Failures > s.policy.ResetLimit
		return nil
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao consultar recuperação", "component", "auth")
		return false, ErrUnavailable
	}
	return suppressed, nil
}

func firstActive(rows []db.LoginMembership) (db.LoginMembership, bool) {
	for _, row := range rows {
		if row.Status == "active" && row.TenantStatus == "active" {
			return row, true
		}
	}
	return db.LoginMembership{}, false
}

func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > 320 || strings.ContainsAny(email, " \t\r\n") {
		return "", ErrInvalidEmail
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at != strings.LastIndexByte(email, '@') || at == len(email)-1 {
		return "", ErrInvalidEmail
	}
	return email, nil
}

func normalizeIP(value string) string {
	parsed := net.ParseIP(strings.TrimSpace(value))
	if parsed == nil {
		return "invalid"
	}
	return parsed.String()
}

func validRole(role string) bool {
	switch role {
	case "admin", "supervisor", "agent":
		return true
	default:
		return false
	}
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
