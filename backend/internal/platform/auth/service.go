package auth

import (
	"chatisp/internal/platform/logging"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/mail"
)

const (
	purposeInvite = "invite"
	purposeReset  = "password_reset"

	kindLoginAccount = "login_account"
	kindLoginIP      = "login_ip"
	kindResetAccount = "reset_account"
	kindResetIP      = "reset_ip"
	kindTokenIP      = "token_ip"
)

// Policy is the calibrated session and lockout schedule.
type Policy struct {
	Idle              time.Duration
	Absolute          time.Duration
	InviteTTL         time.Duration
	ResetTTL          time.Duration
	LockoutThreshold  int
	LockoutBase       time.Duration
	LockoutMax        time.Duration
	LockoutWindow     time.Duration
	ResetLimit        int
	TokenFailureLimit int
}

// DefaultPolicy is the production schedule.
// Lockout grows from LockoutBase and doubles on each further failure, capped by LockoutMax.
var DefaultPolicy = Policy{
	Idle:              30 * time.Minute,
	Absolute:          12 * time.Hour,
	InviteTTL:         48 * time.Hour,
	ResetTTL:          30 * time.Minute,
	LockoutThreshold:  5,
	LockoutBase:       time.Minute,
	LockoutMax:        time.Hour,
	LockoutWindow:     15 * time.Minute,
	ResetLimit:        5,
	TokenFailureLimit: 8,
}

// Principal is the authenticated panel actor. It carries no cookie secret.
type Principal struct {
	SessionID      uuid.UUID
	UserID         uuid.UUID
	Email          string
	TenantID       uuid.UUID
	MembershipID   uuid.UUID
	Role           string
	ExpiresAt      time.Time
	EnrollmentOnly bool
	IsSuperadmin   bool
}

// IssuedSession is returned once. Token is the cookie value and is not stored.
type IssuedSession struct {
	Principal          Principal
	Token              string
	CSRFToken          string
	ExpiresAt          time.Time
	EnrollmentRequired bool
}

// SessionInfo is one live session an administrator may revoke.
type SessionInfo struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
	Current   bool
}

// Invite is the public result of creating an invitation. The token is only in the e-mail.
type Invite struct {
	ExpiresAt time.Time
}

var (
	ErrInvalidCredentials = errors.New("credenciais inválidas")
	ErrLocked             = errors.New("muitas tentativas; tente novamente mais tarde")
	ErrUnauthenticated    = errors.New("não autenticado")
	ErrCSRF               = errors.New("token de CSRF ausente ou inválido")
	ErrForbidden          = errors.New("acesso negado")
	ErrInvalidToken       = errors.New("token inválido ou expirado")
	ErrConflict           = errors.New("usuário já participa deste provedor")
	ErrInvalidPassword    = errors.New("a senha não atende à política")
	ErrInvalidEmail       = errors.New("e-mail inválido")
	ErrInvalidRole        = errors.New("papel inválido")
	ErrMail               = errors.New("não foi possível enviar o e-mail")
	ErrUnavailable        = errors.New("autenticação indisponível")
	ErrTenantRequired     = errors.New("selecione o provedor")
	ErrMFARequired        = errors.New("informe o código de autenticação")
	ErrInvalidMFA         = errors.New("código de autenticação inválido")
	ErrEnrollmentRequired = errors.New("conclua o cadastro do MFA antes de acessar o painel")
	ErrMFAAlready         = errors.New("o MFA já está ativo")
)

// Options wires the identity service. Now defaults to the wall clock.
type Options struct {
	Store  *db.Store
	Mail   mail.Sender
	Logger *slog.Logger
	Policy Policy
	Now    func() time.Time
	Keys   *crypto.Ring
	// PanelURL is the panel origin (https://host[:port]) used to build the
	// links in invitation and password-reset e-mails. Empty sends the token only.
	PanelURL string
}

// Service is the panel identity use-case. It does not log secrets.
type Service struct {
	store  *db.Store
	mail   mail.Sender
	log    *slog.Logger
	policy Policy
	now    func() time.Time
	dummy  string
	keys   *crypto.Ring
	panel  string
}

// New prepares the service and a dummy Argon2id hash used when the account has no password.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, ErrUnavailable
	}
	dummy, err := dummyPasswordHash()
	if err != nil {
		return nil, err
	}
	sender := opts.Mail
	if sender == nil {
		sender = mail.Unavailable{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		store:  opts.Store,
		mail:   sender,
		log:    logger,
		policy: opts.Policy.normalized(),
		now:    now,
		dummy:  dummy,
		keys:   opts.Keys,
		panel:  panelBase(opts.PanelURL),
	}, nil
}

func (p Policy) normalized() Policy {
	base := DefaultPolicy
	if p.Idle <= 0 {
		p.Idle = base.Idle
	}
	if p.Absolute <= 0 {
		p.Absolute = base.Absolute
	}
	if p.InviteTTL <= 0 {
		p.InviteTTL = base.InviteTTL
	}
	if p.ResetTTL <= 0 {
		p.ResetTTL = base.ResetTTL
	}
	if p.LockoutThreshold <= 0 {
		p.LockoutThreshold = base.LockoutThreshold
	}
	if p.LockoutBase <= 0 {
		p.LockoutBase = base.LockoutBase
	}
	if p.LockoutMax <= 0 {
		p.LockoutMax = base.LockoutMax
	}
	if p.LockoutWindow <= 0 {
		p.LockoutWindow = base.LockoutWindow
	}
	if p.ResetLimit <= 0 {
		p.ResetLimit = base.ResetLimit
	}
	if p.TokenFailureLimit <= 0 {
		p.TokenFailureLimit = base.TokenFailureLimit
	}
	return p
}

var (
	dummyOnce sync.Once
	dummyHash string
	dummyErr  error
)

func dummyPasswordHash() (string, error) {
	dummyOnce.Do(func() {
		raw, _, err := NewSecret()
		if err != nil {
			dummyErr = errors.New("falha ao preparar verificação de senha")
			return
		}
		// NewSecret is 43 characters and satisfies the password policy.
		dummyHash, dummyErr = HashPassword(raw)
		if dummyErr != nil {
			dummyErr = errors.New("falha ao preparar verificação de senha")
		}
	})
	if dummyErr != nil {
		return "", dummyErr
	}
	return dummyHash, nil
}

// Login checks the password, the progressive lock and opens a session.
func (s *Service) Login(ctx context.Context, email, password, tenantRaw, ip string) (IssuedSession, error) {
	return s.LoginCode(ctx, email, password, tenantRaw, ip, "")
}

// LoginCode is Login plus a TOTP or recovery code when MFA is already confirmed.
func (s *Service) LoginCode(ctx context.Context, email, password, tenantRaw, ip, code string) (IssuedSession, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return IssuedSession{}, ErrInvalidEmail
	}
	locked, err := s.loginLocked(ctx, normalized, ip)
	if err != nil {
		return IssuedSession{}, err
	}
	if locked {
		s.log.InfoContext(ctx, "login bloqueado", "component", "auth")
		return IssuedSession{}, ErrLocked
	}
	identity, found, err := s.loadIdentity(ctx, normalized)
	if err != nil {
		return IssuedSession{}, err
	}
	hash := s.dummy
	if found && identity.PasswordHash != "" && identity.Status == "active" {
		hash = identity.PasswordHash
	}
	match := false
	if PasswordAcceptable(password) {
		ok, verifyErr := VerifyPassword(password, hash)
		if verifyErr != nil {
			ok = false
		}
		match = ok
	}
	if !found || identity.PasswordHash == "" || identity.Status != "active" {
		match = false
	}
	if !match {
		if err := s.recordLoginFailure(ctx, normalized, ip); err != nil {
			return IssuedSession{}, err
		}
		s.auditLoginFailure(ctx, identity, found)
		s.log.InfoContext(ctx, "login recusado", "component", "auth")
		return IssuedSession{}, ErrInvalidCredentials
	}
	membership, err := chooseMembership(identity.Memberships, tenantRaw)
	if err != nil {
		return IssuedSession{}, err
	}
	return s.finishLogin(ctx, identity, membership, normalized, ip, code)
}

// Logout revokes the session that owns the cookie. An unknown cookie still clears it.
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		return s.store.RevokeSessionByHash(ctx, HashSecret(rawToken), s.now())
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao encerrar sessão", "component", "auth")
		return ErrUnavailable
	}
	s.log.InfoContext(ctx, "logout", "component", "auth")
	return nil
}

// Authenticate loads the session for a cookie. Writes must present the bound CSRF token.
// A revoked, idle or absolutely expired session is rejected on this request.
func (s *Service) Authenticate(ctx context.Context, rawToken, csrf string, requireCSRF bool) (Principal, error) {
	if rawToken == "" {
		return Principal{}, ErrUnauthenticated
	}
	var record db.SessionRecord
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		record, err = s.store.LoadSession(ctx, HashSecret(rawToken), s.now())
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao ler sessão", "component", "auth")
		return Principal{}, ErrUnavailable
	}
	var gate db.SessionGate
	if record.Status == "active" {
		err = s.store.WithAuth(ctx, func(ctx context.Context) error {
			var err error
			gate, err = s.store.SessionGate(ctx, HashSecret(rawToken))
			return err
		})
		if err != nil {
			s.log.ErrorContext(ctx, "falha ao ler sessão", "component", "auth")
			return Principal{}, ErrUnavailable
		}
	}
	switch record.Status {
	case "active":
	case "blocked":
		return Principal{}, ErrForbidden
	default:
		return Principal{}, ErrUnauthenticated
	}
	if requireCSRF {
		if csrf == "" || !SecretEqual(HashSecret(csrf), record.CSRFHash) {
			s.log.InfoContext(ctx, "csrf recusado", "component", "auth")
			return Principal{}, ErrCSRF
		}
	}
	idle := s.now().Add(s.policy.Idle)
	if idle.After(record.AbsoluteExpiresAt) {
		idle = record.AbsoluteExpiresAt
	}
	var touched bool
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		touched, err = s.store.TouchSession(ctx, record.ID, s.now(), idle)
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao renovar sessão", "component", "auth")
		return Principal{}, ErrUnavailable
	}
	if !touched {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{
		SessionID:      record.ID,
		UserID:         record.UserID,
		Email:          record.Email,
		TenantID:       record.TenantID,
		MembershipID:   record.MembershipID,
		Role:           record.Role,
		ExpiresAt:      record.AbsoluteExpiresAt,
		EnrollmentOnly: s.enrollmentOnly(record.Role, gate),
		IsSuperadmin:   gate.IsSuperadmin,
	}, nil
}

// Invite creates a single-use invitation and sends it. The token is not returned.
func (s *Service) Invite(ctx context.Context, actor Principal, email, role string) (Invite, error) {
	ctx = logging.WithTenant(ctx, actor.TenantID)
	if err := s.requireOperational(ctx, actor); err != nil {
		return Invite{}, err
	}
	if !RoleAllows(actor.Role, ActionInvite) {
		return Invite{}, ErrForbidden
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return Invite{}, ErrInvalidEmail
	}
	if !validRole(role) {
		return Invite{}, ErrInvalidRole
	}
	userID, expires, err := s.sendInvite(ctx, actor.TenantID, normalized, role)
	if err != nil {
		return Invite{}, err
	}
	s.audit(ctx, actor.TenantID, actor.UserID, actor.MembershipID, "invitation.create", "user", &userID, "success")
	s.log.InfoContext(ctx, "convite enviado", "component", "auth")
	return Invite{ExpiresAt: expires}, nil
}

// sendInvite prepares the user and the invited membership, stores a single-use token and mails it.
// Callers check permission and write their own audit record.
func (s *Service) sendInvite(ctx context.Context, tenantID uuid.UUID, normalized, role string) (uuid.UUID, time.Time, error) {
	user, err := s.ensureUser(ctx, normalized)
	if err != nil {
		return uuid.Nil, time.Time{}, err
	}
	if user.Status == "disabled" {
		return uuid.Nil, time.Time{}, ErrForbidden
	}
	membership, err := s.ensureInvitedMembership(ctx, tenantID, user.ID, role)
	if err != nil {
		return uuid.Nil, time.Time{}, err
	}
	raw, hash, err := NewSecret()
	if err != nil {
		return uuid.Nil, time.Time{}, ErrUnavailable
	}
	now := s.now()
	expires := now.Add(s.policy.InviteTTL)
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		return s.store.InsertAuthToken(ctx, db.NewAuthToken{
			ID:           uuid.New(),
			TokenHash:    hash,
			Purpose:      purposeInvite,
			UserID:       user.ID,
			TenantID:     tenantID,
			MembershipID: membership.ID,
			ExpiresAt:    expires,
			Now:          now,
		})
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao gravar convite", "component", "auth")
		return uuid.Nil, time.Time{}, ErrUnavailable
	}
	message := mail.Message{
		To:      normalized,
		Subject: "Convite para o Chat ISP",
		Text: "Você recebeu um convite para acessar o Chat ISP.\n" +
			s.linkLine("/convite", raw) +
			"O token abaixo só pode ser usado uma vez e deixa de valer no prazo informado.\n\n" +
			raw + "\n",
	}
	if err := s.mail.Send(ctx, message); err != nil {
		_ = s.store.WithAuth(ctx, func(ctx context.Context) error {
			return s.store.InvalidateAuthToken(ctx, hash, s.now())
		})
		s.log.ErrorContext(ctx, "falha ao enviar convite", "component", "auth")
		return uuid.Nil, time.Time{}, ErrMail
	}
	return user.ID, expires, nil
}

// AcceptInvite consumes an invite and opens a session.
func (s *Service) AcceptInvite(ctx context.Context, token, password, ip string) (IssuedSession, error) {
	if err := s.guardTokenIP(ctx, ip); err != nil {
		return IssuedSession{}, err
	}
	if !PasswordAcceptable(password) {
		return IssuedSession{}, ErrInvalidPassword
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return IssuedSession{}, ErrInvalidPassword
	}
	var result db.TokenResult
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.store.AcceptInvite(ctx, HashSecret(token), passwordHash, s.now())
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao aceitar convite", "component", "auth")
		return IssuedSession{}, ErrUnavailable
	}
	if result.Status != "ok" {
		s.noteTokenFailure(ctx, ip)
		s.log.InfoContext(ctx, "convite recusado", "component", "auth")
		if result.Status == "blocked" {
			return IssuedSession{}, ErrForbidden
		}
		return IssuedSession{}, ErrInvalidToken
	}
	_ = s.clearKind(ctx, kindTokenIP, ip)
	issued, err := s.issueForMembership(ctx, result.UserID, result.TenantID, result.MembershipID, passwordHash)
	if err != nil {
		return IssuedSession{}, err
	}
	s.audit(ctx, result.TenantID, result.UserID, result.MembershipID, "invitation.accept", "user", &result.UserID, "success")
	s.log.InfoContext(ctx, "convite aceito", "component", "auth")
	return issued, nil
}

// RequestPasswordReset sends a single-use token when the account can use one.
// The result is the same whether or not the e-mail exists.
func (s *Service) RequestPasswordReset(ctx context.Context, email, ip string) error {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return nil
	}
	suppressed, err := s.resetSuppressed(ctx, normalized, ip)
	if err != nil {
		return err
	}
	if suppressed {
		s.log.InfoContext(ctx, "recuperação limitada", "component", "auth")
		return nil
	}
	identity, found, err := s.loadIdentity(ctx, normalized)
	if err != nil {
		return err
	}
	if !found || identity.Status != "active" || identity.PasswordHash == "" {
		s.log.InfoContext(ctx, "recuperação ignorada", "component", "auth")
		return nil
	}
	raw, hash, err := NewSecret()
	if err != nil {
		return ErrUnavailable
	}
	now := s.now()
	expires := now.Add(s.policy.ResetTTL)
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		return s.store.InsertAuthToken(ctx, db.NewAuthToken{
			ID:        uuid.New(),
			TokenHash: hash,
			Purpose:   purposeReset,
			UserID:    identity.UserID,
			ExpiresAt: expires,
			Now:       now,
		})
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao gravar recuperação", "component", "auth")
		return ErrUnavailable
	}
	message := mail.Message{
		To:      normalized,
		Subject: "Recuperação de senha do Chat ISP",
		Text: "Recebemos um pedido para redefinir a senha do Chat ISP.\n" +
			s.linkLine("/recuperar-senha/confirmar", raw) +
			"O token abaixo só pode ser usado uma vez e deixa de valer no prazo curto desta mensagem.\n" +
			"Se você não pediu, ignore este e-mail.\n\n" +
			raw + "\n",
	}
	if err := s.mail.Send(ctx, message); err != nil {
		_ = s.store.WithAuth(ctx, func(ctx context.Context) error {
			return s.store.InvalidateAuthToken(ctx, hash, s.now())
		})
		s.log.ErrorContext(ctx, "falha ao enviar recuperação", "component", "auth")
		return ErrMail
	}
	if membership, ok := firstActive(identity.Memberships); ok {
		s.audit(ctx, membership.TenantID, identity.UserID, membership.MembershipID, "password_reset.request", "user", &identity.UserID, "success")
	}
	s.log.InfoContext(ctx, "recuperação enviada", "component", "auth")
	return nil
}

// ConfirmPasswordReset consumes a reset token, replaces the password and revokes sessions.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, password, ip string) error {
	if err := s.guardTokenIP(ctx, ip); err != nil {
		return err
	}
	if !PasswordAcceptable(password) {
		return ErrInvalidPassword
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return ErrInvalidPassword
	}
	var result db.TokenResult
	err = s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.store.ConfirmPasswordReset(ctx, HashSecret(token), passwordHash, s.now())
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao confirmar recuperação", "component", "auth")
		return ErrUnavailable
	}
	if result.Status != "ok" {
		s.noteTokenFailure(ctx, ip)
		s.log.InfoContext(ctx, "recuperação recusada", "component", "auth")
		if result.Status == "blocked" {
			return ErrForbidden
		}
		return ErrInvalidToken
	}
	_ = s.clearKind(ctx, kindTokenIP, ip)
	s.auditPasswordReset(ctx, result.UserID)
	s.log.InfoContext(ctx, "senha redefinida", "component", "auth")
	return nil
}

// ListSessions returns live sessions of the actor's tenant. Only an admin may call it.
func (s *Service) ListSessions(ctx context.Context, actor Principal) ([]SessionInfo, error) {
	ctx = logging.WithTenant(ctx, actor.TenantID)
	if err := s.requireOperational(ctx, actor); err != nil {
		return nil, err
	}
	if !RoleAllows(actor.Role, ActionSessionList) {
		return nil, ErrForbidden
	}
	var rows []db.ListedSession
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		rows, err = s.store.ListTenantSessions(ctx, actor.TenantID, s.now())
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao listar sessões", "component", "auth")
		return nil, ErrUnavailable
	}
	out := make([]SessionInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, SessionInfo{
			ID:        row.ID,
			UserID:    row.UserID,
			CreatedAt: row.CreatedAt,
			ExpiresAt: row.AbsoluteExpiresAt,
			Current:   row.ID == actor.SessionID,
		})
	}
	return out, nil
}

// RevokeSession revokes one session of the actor's tenant.
func (s *Service) RevokeSession(ctx context.Context, actor Principal, sessionID uuid.UUID) error {
	ctx = logging.WithTenant(ctx, actor.TenantID)
	if err := s.requireOperational(ctx, actor); err != nil {
		return err
	}
	if sessionID == uuid.Nil {
		return ErrUnauthenticated
	}
	var status string
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		status, err = s.store.RevokeSessionForActor(ctx, sessionID, actor.TenantID, actor.UserID, s.now())
		return err
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao revogar sessão", "component", "auth")
		return ErrUnavailable
	}
	switch status {
	case "ok":
		s.audit(ctx, actor.TenantID, actor.UserID, actor.MembershipID, "session.revoke", "session", &sessionID, "success")
		s.log.InfoContext(ctx, "sessão revogada", "component", "auth")
		return nil
	case "forbidden":
		return ErrForbidden
	default:
		return ErrUnauthenticated
	}
}

func (s *Service) issueSession(ctx context.Context, userID uuid.UUID, email string, membership db.LoginMembership, clearAccount, assurance, verifiedHash string, record db.MFARecord) (IssuedSession, error) {
	raw, tokenHash, err := NewSecret()
	if err != nil {
		return IssuedSession{}, ErrUnavailable
	}
	csrf, csrfHash, err := NewSecret()
	if err != nil {
		return IssuedSession{}, ErrUnavailable
	}
	now := s.now()
	absolute := now.Add(s.policy.Absolute)
	idle := now.Add(s.policy.Idle)
	if idle.After(absolute) {
		idle = absolute
	}
	if assurance != "enrollment" {
		assurance = "full"
	}
	sessionID := uuid.New()
	// Serialize session creation with tenant suspension and membership changes.
	// The auth insert rechecks their state after this lock is acquired.
	err = s.store.WithTenant(ctx, membership.TenantID, func(ctx context.Context) error {
		if err := s.store.LockTenant(ctx); err != nil {
			return err
		}
		return s.store.WithAuthInTenant(ctx, func(ctx context.Context) error {
			current, err := s.store.VerifiedPasswordCurrent(ctx, userID, verifiedHash, record)
			if err != nil {
				return err
			}
			if !current {
				return ErrInvalidCredentials
			}
			if clearAccount != "" {
				if err := s.store.ClearFailures(ctx, kindLoginAccount, SubjectHash(kindLoginAccount, clearAccount)); err != nil {
					return err
				}
			}
			if err := s.store.InsertSession(ctx, db.NewSession{
				ID:                sessionID,
				TokenHash:         tokenHash,
				UserID:            userID,
				TenantID:          membership.TenantID,
				MembershipID:      membership.MembershipID,
				CSRFHash:          csrfHash,
				Now:               now,
				IdleExpiresAt:     idle,
				AbsoluteExpiresAt: absolute,
			}); err != nil {
				return err
			}
			if assurance == "enrollment" {
				return s.store.SetSessionAssurance(ctx, sessionID, assurance)
			}
			return nil
		})
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return IssuedSession{}, ErrInvalidCredentials
		}
		s.log.ErrorContext(ctx, "falha ao abrir sessão", "component", "auth")
		return IssuedSession{}, ErrUnavailable
	}
	return IssuedSession{
		Principal: Principal{
			SessionID:    sessionID,
			UserID:       userID,
			Email:        email,
			TenantID:     membership.TenantID,
			MembershipID: membership.MembershipID,
			Role:         membership.Role,
			ExpiresAt:    absolute,
		},
		Token:     raw,
		CSRFToken: csrf,
		ExpiresAt: absolute,
	}, nil
}

func (s *Service) issueForMembership(ctx context.Context, userID, tenantID, membershipID uuid.UUID, verifiedHash string) (IssuedSession, error) {
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
		s.log.ErrorContext(ctx, "falha ao concluir sessão do convite", "component", "auth")
		return IssuedSession{}, ErrUnavailable
	}
	var role string
	err = s.store.WithTenant(ctx, tenantID, func(ctx context.Context) error {
		row, err := s.store.FindMembershipByUser(ctx, userID)
		if err != nil {
			return err
		}
		if row.ID != membershipID || row.Status != "active" {
			return db.ErrNotFound
		}
		role = row.Role
		return nil
	})
	if err != nil {
		s.log.ErrorContext(ctx, "falha ao concluir sessão do convite", "component", "auth")
		return IssuedSession{}, ErrUnavailable
	}
	assurance := "full"
	record, err := s.mfaRecord(ctx, userID)
	if err != nil {
		return IssuedSession{}, err
	}
	if s.keys != nil {
		if record.Confirmed {
			return IssuedSession{}, ErrMFARequired
		}
		if role == "admin" || record.IsSuperadmin {
			assurance = "enrollment"
		}
	}
	issued, err := s.issueSession(ctx, userID, email, db.LoginMembership{
		TenantID:     tenantID,
		MembershipID: membershipID,
		Role:         role,
		Status:       "active",
		TenantStatus: "active",
	}, email, assurance, verifiedHash, record)
	if err != nil {
		return IssuedSession{}, err
	}
	issued.EnrollmentRequired = assurance == "enrollment"
	return issued, nil
}

// panelBase accepts only an https origin without path, query or user info.
func panelBase(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return "https://" + u.Host
}

// linkLine returns the e-mail line with the panel link for this token, or
// nothing when no panel origin is configured.
func (s *Service) linkLine(path, token string) string {
	if s.panel == "" {
		return ""
	}
	return "Abra: " + s.panel + path + "?token=" + url.QueryEscape(token) + "\n"
}
