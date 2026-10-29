package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
)

const (
	kindMFAAccount = "mfa_account"
	kindMFAIP      = "mfa_ip"

	recoveryCodeCount = 10
	totpIssuer        = "Chat ISP"
	totpField         = "totp_secret"
	totpEntity        = "user"
)

// Enrollment is shown once. The secret and the recovery codes are not stored in clear text.
type Enrollment struct {
	OTPAuthURL    string
	Secret        string
	RecoveryCodes []string
}

// BeginEnrollment seals a new TOTP secret and returns recovery codes a single time.
func (s *Service) BeginEnrollment(ctx context.Context, actor Principal) (Enrollment, error) {
	if actor.UserID == uuid.Nil || actor.TenantID == uuid.Nil {
		return Enrollment{}, ErrUnauthenticated
	}
	if !RoleAllows(actor.Role, ActionMFAEnroll) {
		return Enrollment{}, ErrForbidden
	}
	if s.keys == nil {
		return Enrollment{}, ErrUnavailable
	}
	record, err := s.mfaRecord(ctx, actor.UserID)
	if err != nil {
		return Enrollment{}, err
	}
	if record.Confirmed {
		return Enrollment{}, ErrMFAAlready
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: actor.Email,
		Period:      30,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
		Rand:        rand.Reader,
	})
	if err != nil {
		s.log.Error("falha ao gerar TOTP", "component", "auth")
		return Enrollment{}, ErrUnavailable
	}
	sealed, err := s.keys.Encrypt([]byte(key.Secret()), crypto.Context{
		TenantID: actor.TenantID.String(),
		Entity:   totpEntity,
		EntityID: actor.UserID.String(),
		Field:    totpField,
	})
	if err != nil {
		s.log.Error("falha ao cifrar TOTP", "component", "auth")
		return Enrollment{}, ErrUnavailable
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return Enrollment{}, ErrUnavailable
	}
	now := s.now()
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		if err := s.store.BeginMFA(ctx, actor.UserID, actor.TenantID, sealed.Ciphertext, sealed.Version); err != nil {
			return err
		}
		for i, hash := range hashes {
			if err := s.store.AddRecoveryCode(ctx, uuid.New(), actor.UserID, hash, now.Add(time.Duration(i))); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.log.Error("falha ao gravar MFA", "component", "auth")
		return Enrollment{}, ErrUnavailable
	}
	return Enrollment{OTPAuthURL: key.URL(), Secret: key.Secret(), RecoveryCodes: codes}, nil
}

// ConfirmEnrollment checks the first TOTP code and opens the panel for this session.
func (s *Service) ConfirmEnrollment(ctx context.Context, actor Principal, code string) error {
	if actor.UserID == uuid.Nil {
		return ErrUnauthenticated
	}
	if !RoleAllows(actor.Role, ActionMFAEnroll) {
		return ErrForbidden
	}
	if s.keys == nil {
		return ErrUnavailable
	}
	record, err := s.mfaRecord(ctx, actor.UserID)
	if err != nil {
		return err
	}
	if record.Confirmed {
		return ErrMFAAlready
	}
	if len(record.Ciphertext) == 0 {
		return ErrInvalidMFA
	}
	locked, err := s.mfaLocked(ctx, actor.UserID, "")
	if err != nil {
		return err
	}
	if locked {
		return ErrLocked
	}
	secret, err := s.decryptSecret(record, actor.UserID)
	if err != nil {
		return ErrUnavailable
	}
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secret, s.now(), totpValidate())
	if err != nil || !ok {
		if recErr := s.recordMFAFailure(ctx, actor.UserID, ""); recErr != nil {
			return recErr
		}
		s.audit(ctx, actor.TenantID, actor.UserID, actor.MembershipID, "mfa.verify", "user", &actor.UserID, "denied")
		return ErrInvalidMFA
	}
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		confirmed, err := s.store.ConfirmMFA(ctx, actor.UserID, s.now())
		if err != nil {
			return err
		}
		if !confirmed {
			return db.ErrNotFound
		}
		if actor.SessionID != uuid.Nil {
			if err := s.store.SetSessionAssurance(ctx, actor.SessionID, "full"); err != nil {
				return err
			}
		}
		return s.clearMFAFailures(ctx, actor.UserID, "")
	})
	if err != nil {
		s.log.Error("falha ao confirmar MFA", "component", "auth")
		return ErrUnavailable
	}
	s.audit(ctx, actor.TenantID, actor.UserID, actor.MembershipID, "mfa.enroll", "user", &actor.UserID, "success")
	if record.IsSuperadmin || actor.IsSuperadmin {
		s.auditPlatform(ctx, actor.UserID, "mfa.enroll", actor.UserID)
	}
	s.log.Info("mfa confirmado", "component", "auth")
	return nil
}

// ResetMFA clears another user's factor, or the actor's own, and revokes their sessions.
// The actor must already have a full panel session. The reset is audited.
func (s *Service) ResetMFA(ctx context.Context, actor Principal, target uuid.UUID) error {
	if err := s.requireOperational(ctx, actor); err != nil {
		return err
	}
	if !RoleAllows(actor.Role, ActionMFAReset) && !actor.IsSuperadmin {
		return ErrForbidden
	}
	if s.keys == nil {
		return ErrUnavailable
	}
	if target == uuid.Nil {
		return ErrForbidden
	}
	var membership db.Membership
	err := s.store.WithTenant(ctx, actor.TenantID, func(ctx context.Context) error {
		var err error
		membership, err = s.store.FindMembershipByUser(ctx, target)
		return err
	})
	if errors.Is(err, db.ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		s.log.Error("falha ao localizar MFA", "component", "auth")
		return ErrUnavailable
	}
	if membership.Status == "disabled" {
		return ErrForbidden
	}
	record, err := s.mfaRecord(ctx, target)
	if err != nil {
		return err
	}
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		return s.store.ClearMFA(ctx, target, s.now())
	})
	if err != nil {
		s.log.Error("falha ao redefinir MFA", "component", "auth")
		return ErrUnavailable
	}
	s.audit(ctx, actor.TenantID, actor.UserID, actor.MembershipID, "mfa.reset", "user", &target, "success")
	if record.IsSuperadmin || actor.IsSuperadmin {
		s.auditPlatform(ctx, actor.UserID, "mfa.reset", target)
	}
	s.log.Info("mfa redefinido", "component", "auth")
	return nil
}

func (s *Service) finishLogin(ctx context.Context, identity db.LoginIdentity, membership db.LoginMembership, account, ip, code string) (IssuedSession, error) {
	if s.keys == nil {
		return s.issueAndAudit(ctx, identity, membership, account, "full", false, false)
	}
	record, err := s.mfaRecord(ctx, identity.UserID)
	if err != nil {
		return IssuedSession{}, err
	}
	required := mfaRequired(identity.Memberships, membership, record.IsSuperadmin)
	if record.Confirmed {
		if strings.TrimSpace(code) == "" {
			return IssuedSession{}, ErrMFARequired
		}
		recovery, err := s.verifyFactor(ctx, identity.UserID, membership, record, code, ip)
		if err != nil {
			return IssuedSession{}, err
		}
		issued, err := s.issueAndAudit(ctx, identity, membership, account, "full", false, false)
		if err != nil {
			return IssuedSession{}, err
		}
		if recovery {
			s.audit(ctx, membership.TenantID, identity.UserID, membership.MembershipID, "mfa.recovery", "user", &identity.UserID, "success")
			if record.IsSuperadmin {
				s.auditPlatform(ctx, identity.UserID, "mfa.recovery", identity.UserID)
			}
		}
		return issued, nil
	}
	if required {
		return s.issueAndAudit(ctx, identity, membership, account, "enrollment", true, false)
	}
	return s.issueAndAudit(ctx, identity, membership, account, "full", false, false)
}

func (s *Service) issueAndAudit(ctx context.Context, identity db.LoginIdentity, membership db.LoginMembership, account, assurance string, enrollment, skipAudit bool) (IssuedSession, error) {
	issued, err := s.issueSession(ctx, identity.UserID, identity.Email, membership, account, assurance)
	if err != nil {
		return IssuedSession{}, err
	}
	issued.EnrollmentRequired = enrollment
	if !skipAudit {
		result := "success"
		if enrollment {
			result = "enrollment"
		}
		s.audit(ctx, membership.TenantID, issued.Principal.UserID, issued.Principal.MembershipID, "login", "session", &issued.Principal.SessionID, result)
	}
	s.log.Info("login aceito", "component", "auth")
	return issued, nil
}

func (s *Service) verifyFactor(ctx context.Context, userID uuid.UUID, membership db.LoginMembership, record db.MFARecord, code, ip string) (bool, error) {
	locked, err := s.mfaLocked(ctx, userID, ip)
	if err != nil {
		return false, err
	}
	if locked {
		return false, ErrLocked
	}
	normalized := normalizeRecovery(code)
	recovery := false
	ok := false
	if isTOTPCode(code) {
		secret, err := s.decryptSecret(record, userID)
		if err != nil {
			return false, ErrUnavailable
		}
		match, err := totp.ValidateCustom(strings.TrimSpace(code), secret, s.now(), totpValidate())
		if err != nil {
			match = false
		}
		ok = match
	} else if normalized != "" {
		consumed, err := s.consumeRecovery(ctx, userID, normalized)
		if err != nil {
			return false, err
		}
		ok = consumed
		recovery = consumed
	}
	if !ok {
		if err := s.recordMFAFailure(ctx, userID, ip); err != nil {
			return false, err
		}
		s.audit(ctx, membership.TenantID, userID, membership.MembershipID, "mfa.verify", "user", &userID, "denied")
		return false, ErrInvalidMFA
	}
	if err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		return s.clearMFAFailures(ctx, userID, ip)
	}); err != nil {
		s.log.Error("falha ao limpar tentativas de MFA", "component", "auth")
		return false, ErrUnavailable
	}
	return recovery, nil
}

func (s *Service) consumeRecovery(ctx context.Context, userID uuid.UUID, normalized string) (bool, error) {
	sum := HashSecret(normalized)
	var codes []db.RecoveryCode
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		codes, err = s.store.ListRecoveryCodes(ctx, userID)
		return err
	})
	if err != nil {
		s.log.Error("falha ao ler códigos de recuperação", "component", "auth")
		return false, ErrUnavailable
	}
	var matched uuid.UUID
	for _, row := range codes {
		if SecretEqual(sum, row.Hash) {
			matched = row.ID
		}
	}
	if matched == uuid.Nil {
		return false, nil
	}
	var consumed bool
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		consumed, err = s.store.ConsumeRecoveryCode(ctx, userID, matched, s.now())
		return err
	})
	if err != nil {
		s.log.Error("falha ao consumir código de recuperação", "component", "auth")
		return false, ErrUnavailable
	}
	return consumed, nil
}

func (s *Service) decryptSecret(record db.MFARecord, userID uuid.UUID) (string, error) {
	if s.keys == nil || len(record.Ciphertext) == 0 || record.AADTenant == uuid.Nil {
		return "", errors.New("segredo ausente")
	}
	plain, err := s.keys.Decrypt(record.KeyVersion, record.Ciphertext, crypto.Context{
		TenantID: record.AADTenant.String(),
		Entity:   totpEntity,
		EntityID: userID.String(),
		Field:    totpField,
	})
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) mfaRecord(ctx context.Context, userID uuid.UUID) (db.MFARecord, error) {
	var record db.MFARecord
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		record, err = s.store.MFARecord(ctx, userID)
		return err
	})
	if err != nil {
		s.log.Error("falha ao ler MFA", "component", "auth")
		return db.MFARecord{}, ErrUnavailable
	}
	return record, nil
}

func (s *Service) mfaLocked(ctx context.Context, userID uuid.UUID, ip string) (bool, error) {
	var locked bool
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		checks := []struct {
			kind  string
			value string
		}{{kindMFAAccount, userID.String()}}
		if strings.TrimSpace(ip) != "" {
			checks = append(checks, struct {
				kind  string
				value string
			}{kindMFAIP, normalizeIP(ip)})
		}
		for _, item := range checks {
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
		s.log.Error("falha ao consultar bloqueio de MFA", "component", "auth")
		return false, ErrUnavailable
	}
	return locked, nil
}

func (s *Service) recordMFAFailure(ctx context.Context, userID uuid.UUID, ip string) error {
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		if err := s.bumpLock(ctx, kindMFAAccount, userID.String(), s.policy.LockoutThreshold); err != nil {
			return err
		}
		if strings.TrimSpace(ip) == "" {
			return nil
		}
		return s.bumpLock(ctx, kindMFAIP, normalizeIP(ip), s.policy.LockoutThreshold)
	})
	if err != nil {
		s.log.Error("falha ao registrar tentativa de MFA", "component", "auth")
		return ErrUnavailable
	}
	return nil
}

func (s *Service) clearMFAFailures(ctx context.Context, userID uuid.UUID, ip string) error {
	if err := s.store.ClearFailures(ctx, kindMFAAccount, SubjectHash(kindMFAAccount, userID.String())); err != nil {
		return err
	}
	if strings.TrimSpace(ip) == "" {
		return nil
	}
	return s.store.ClearFailures(ctx, kindMFAIP, SubjectHash(kindMFAIP, normalizeIP(ip)))
}

func (s *Service) requirePanel(actor Principal) error {
	if actor.EnrollmentOnly {
		return ErrEnrollmentRequired
	}
	return nil
}

func (s *Service) auditPlatform(ctx context.Context, actor uuid.UUID, action string, target uuid.UUID) {
	targetID := target.String()
	err := s.store.WithPlatform(ctx, func(ctx context.Context) error {
		_, err := s.store.CreatePlatformAuditLog(ctx, db.NewPlatformAuditLog{
			Actor:      actor.String(),
			Action:     action,
			TargetType: "user",
			TargetID:   &targetID,
			Result:     "success",
		})
		return err
	})
	if err != nil {
		s.log.Error("falha ao auditar plataforma", "component", "auth")
	}
}

func (s *Service) enrollmentOnly(role string, gate db.SessionGate) bool {
	if s.keys == nil || !gate.Found {
		return false
	}
	if gate.Assurance == "enrollment" {
		return true
	}
	if !gate.Confirmed && (role == "admin" || gate.IsSuperadmin) {
		return true
	}
	return false
}

func mfaRequired(rows []db.LoginMembership, chosen db.LoginMembership, super bool) bool {
	if super || chosen.Role == "admin" {
		return true
	}
	for _, row := range rows {
		if row.Status == "active" && row.TenantStatus == "active" && row.Role == "admin" {
			return true
		}
	}
	return false
}

func totpValidate() totp.ValidateOpts {
	return totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	}
}

func isTOTPCode(code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizeRecovery(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(code)) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) != 20 {
		return ""
	}
	for _, r := range out {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return ""
		}
	}
	return out
}

func newRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, 0, recoveryCodeCount)
	hashes := make([][]byte, 0, recoveryCodeCount)
	seen := map[string]struct{}{}
	for len(codes) < recoveryCodeCount {
		buf := make([]byte, 10)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, err
		}
		raw := hex.EncodeToString(buf)
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		display := raw[0:4] + "-" + raw[4:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20]
		codes = append(codes, display)
		hashes = append(hashes, HashSecret(raw))
	}
	return codes, hashes, nil
}
