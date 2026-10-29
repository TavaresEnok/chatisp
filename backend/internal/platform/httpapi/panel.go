package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
)

type clientIPKey struct{}

func registerPanel(api huma.API, deps Dependencies) {
	panel := &panelAPI{auth: deps.Auth}
	huma.Register(api, panelOp("create-panel-session", http.MethodPost, "/api/panel/session", "Entra no painel", "Autentica e-mail e senha e abre uma sessão.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable,
	}), panel.login)
	huma.Register(api, panelOp("delete-panel-session", http.MethodDelete, "/api/panel/session", "Encerra a sessão", "Revoga a sessão do cookie e apaga os cookies.", http.StatusNoContent, []int{
		http.StatusForbidden, http.StatusServiceUnavailable,
	}), panel.logout)
	huma.Register(api, panelOp("get-panel-session", http.MethodGet, "/api/panel/session", "Mostra a sessão atual", "Exige o cookie de sessão.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable,
	}), panel.current)
	huma.Register(api, panelOp("create-panel-invitation", http.MethodPost, "/api/panel/invitations", "Convida um usuário", "Somente administradores do provedor da sessão.", http.StatusCreated, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable,
	}), panel.invite)
	huma.Register(api, panelOp("accept-panel-invitation", http.MethodPost, "/api/panel/invitations/accept", "Aceita um convite", "Consome o token de uso único e abre uma sessão.", http.StatusOK, []int{
		http.StatusBadRequest, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable,
	}), panel.acceptInvite)
	huma.Register(api, panelOp("create-panel-password-reset", http.MethodPost, "/api/panel/password-resets", "Pede recuperação de senha", "A resposta é a mesma quando o e-mail não existe.", http.StatusAccepted, []int{
		http.StatusServiceUnavailable,
	}), panel.requestReset)
	huma.Register(api, panelOp("confirm-panel-password-reset", http.MethodPost, "/api/panel/password-resets/confirm", "Confirma a nova senha", "Consome o token, troca a senha e revoga as sessões.", http.StatusNoContent, []int{
		http.StatusBadRequest, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable,
	}), panel.confirmReset)
	huma.Register(api, panelOp("list-panel-sessions", http.MethodGet, "/api/panel/sessions", "Lista sessões do provedor", "Somente administradores.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable,
	}), panel.listSessions)
	huma.Register(api, panelOp("revoke-panel-session", http.MethodPost, "/api/panel/sessions/{session_id}/revoke", "Revoga uma sessão", "O próprio usuário ou um administrador do mesmo provedor.", http.StatusNoContent, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable,
	}), panel.revoke)
	huma.Register(api, panelOp("begin-panel-mfa", http.MethodPost, "/api/panel/mfa/enrollment", "Inicia o cadastro do MFA", "Gera o segredo TOTP cifrado e os códigos de recuperação, exibidos uma vez.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusServiceUnavailable,
	}), panel.beginMFA)
	huma.Register(api, panelOp("confirm-panel-mfa", http.MethodPost, "/api/panel/mfa/enrollment/confirm", "Confirma o MFA", "Valida o primeiro código TOTP e libera o painel nesta sessão.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusTooManyRequests, http.StatusServiceUnavailable,
	}), panel.confirmMFA)
	huma.Register(api, panelOp("reset-panel-mfa", http.MethodPost, "/api/panel/mfa/reset", "Redefine o MFA", "Administrador com MFA ativo limpa o fator de um usuário do provedor. A ação é auditada.", http.StatusNoContent, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable,
	}), panel.resetMFA)
}

func panelOp(id, method, path, summary, description string, status int, errors []int) huma.Operation {
	return huma.Operation{
		OperationID:   id,
		Method:        method,
		Path:          path,
		Summary:       summary,
		Description:   description,
		Tags:          []string{"painel"},
		DefaultStatus: status,
		MaxBodyBytes:  4096,
		Errors:        errors,
	}
}

type panelAPI struct {
	auth *auth.Service
}

type loginInput struct {
	Body struct {
		Email    string `json:"email" doc:"E-mail da conta." maxLength:"320"`
		Password string `json:"password" doc:"Senha da conta." maxLength:"128"`
		TenantID string `json:"tenant_id,omitempty" doc:"Provedor, quando a conta participa de mais de um." maxLength:"36"`
		Code     string `json:"code,omitempty" doc:"Código TOTP ou de recuperação, quando o MFA já está confirmado." maxLength:"64"`
	}
}

type sessionBody struct {
	UserID                uuid.UUID `json:"user_id" doc:"Usuário autenticado."`
	Email                 string    `json:"email" doc:"E-mail da conta."`
	TenantID              uuid.UUID `json:"tenant_id" doc:"Provedor da sessão."`
	MembershipID          uuid.UUID `json:"membership_id" doc:"Vínculo usado nesta sessão."`
	Role                  string    `json:"role" doc:"Papel no provedor."`
	SessionID             uuid.UUID `json:"session_id" doc:"Identificador da sessão, distinto do cookie."`
	CSRFToken             string    `json:"csrf_token" doc:"Token de CSRF desta sessão. Também vai no cookie legível pelo painel."`
	ExpiresAt             time.Time `json:"expires_at" doc:"Fim absoluto da sessão."`
	MFAEnrollmentRequired bool      `json:"mfa_enrollment_required" doc:"Verdadeiro quando o administrador ainda precisa concluir o MFA."`
}

type sessionOutput struct {
	SetCookie []string `header:"Set-Cookie"`
	Body      sessionBody
}

type cookieInput struct {
	Session string `cookie:"__Host-session" doc:"Cookie de sessão do painel."`
	CSRF    string `header:"X-CSRF-Token" doc:"Token de CSRF vinculado à sessão."`
}

type clearedOutput struct {
	SetCookie []string `header:"Set-Cookie"`
}

type principalBody struct {
	UserID                uuid.UUID `json:"user_id" doc:"Usuário autenticado."`
	Email                 string    `json:"email" doc:"E-mail da conta."`
	TenantID              uuid.UUID `json:"tenant_id" doc:"Provedor da sessão."`
	MembershipID          uuid.UUID `json:"membership_id" doc:"Vínculo usado nesta sessão."`
	Role                  string    `json:"role" doc:"Papel no provedor."`
	SessionID             uuid.UUID `json:"session_id" doc:"Identificador da sessão."`
	ExpiresAt             time.Time `json:"expires_at" doc:"Fim absoluto da sessão."`
	MFAEnrollmentRequired bool      `json:"mfa_enrollment_required" doc:"Verdadeiro quando o administrador ainda precisa concluir o MFA."`
}

type principalOutput struct {
	Body principalBody
}

type inviteInput struct {
	Session string `cookie:"__Host-session" doc:"Cookie de sessão do painel."`
	CSRF    string `header:"X-CSRF-Token" doc:"Token de CSRF vinculado à sessão."`
	Body    struct {
		Email string `json:"email" doc:"E-mail convidado." maxLength:"320"`
		Role  string `json:"role" doc:"Papel concedido." enum:"admin,supervisor,agent"`
	}
}

type inviteBody struct {
	ExpiresAt time.Time `json:"expires_at" doc:"Momento em que o convite deixa de valer."`
}

type inviteOutput struct {
	Body inviteBody
}

type acceptInput struct {
	Body struct {
		Token    string `json:"token" doc:"Token de uso único recebido no convite." maxLength:"128"`
		Password string `json:"password" doc:"Senha inicial da conta." maxLength:"128"`
	}
}

type resetRequestInput struct {
	Body struct {
		Email string `json:"email,omitempty" doc:"E-mail da conta." maxLength:"320"`
	}
}

type acceptedBody struct {
	Status string `json:"status" enum:"accepted" doc:"Pedido recebido." example:"accepted"`
}

type acceptedOutput struct {
	Body acceptedBody
}

type resetConfirmInput struct {
	Body struct {
		Token    string `json:"token" doc:"Token de uso único da recuperação." maxLength:"128"`
		Password string `json:"password" doc:"Nova senha." maxLength:"128"`
	}
}

type sessionInfoBody struct {
	ID        uuid.UUID `json:"id" doc:"Sessão."`
	UserID    uuid.UUID `json:"user_id" doc:"Usuário da sessão."`
	CreatedAt time.Time `json:"created_at" doc:"Abertura da sessão."`
	ExpiresAt time.Time `json:"expires_at" doc:"Fim absoluto da sessão."`
	Current   bool      `json:"current" doc:"Verdadeiro na sessão que fez a consulta."`
}

type sessionListOutput struct {
	Body struct {
		Sessions []sessionInfoBody `json:"sessions" doc:"Sessões ainda válidas do provedor."`
	}
}

type revokeInput struct {
	Session   string    `cookie:"__Host-session" doc:"Cookie de sessão do painel."`
	CSRF      string    `header:"X-CSRF-Token" doc:"Token de CSRF vinculado à sessão."`
	SessionID uuid.UUID `path:"session_id" doc:"Sessão a revogar."`
}

func (p *panelAPI) login(ctx context.Context, input *loginInput) (*sessionOutput, error) {
	if err := p.available(); err != nil {
		return nil, err
	}
	issued, err := p.auth.LoginCode(ctx, input.Body.Email, input.Body.Password, input.Body.TenantID, clientIP(ctx), input.Body.Code)
	if err != nil {
		return nil, authProblem(err)
	}
	return sessionResponse(issued), nil
}

func (p *panelAPI) logout(ctx context.Context, input *cookieInput) (*clearedOutput, error) {
	if err := p.available(); err != nil {
		return nil, err
	}
	if input.Session != "" {
		_, err := p.auth.Authenticate(ctx, input.Session, input.CSRF, true)
		if err != nil && !errors.Is(err, auth.ErrUnauthenticated) && !errors.Is(err, auth.ErrForbidden) {
			return nil, authProblem(err)
		}
		if err := p.auth.Logout(ctx, input.Session); err != nil {
			return nil, authProblem(err)
		}
	}
	return &clearedOutput{SetCookie: clearSessionCookies()}, nil
}

func (p *panelAPI) current(ctx context.Context, input *cookieInput) (*principalOutput, error) {
	principal, err := p.read(ctx, input.Session, input.CSRF, false)
	if err != nil {
		return nil, err
	}
	return &principalOutput{Body: principalFrom(principal)}, nil
}

func (p *panelAPI) invite(ctx context.Context, input *inviteInput) (*inviteOutput, error) {
	actor, err := p.readPanel(ctx, input.Session, input.CSRF, true)
	if err != nil {
		return nil, err
	}
	created, err := p.auth.Invite(ctx, actor, input.Body.Email, input.Body.Role)
	if err != nil {
		return nil, authProblem(err)
	}
	return &inviteOutput{Body: inviteBody{ExpiresAt: created.ExpiresAt}}, nil
}

func (p *panelAPI) acceptInvite(ctx context.Context, input *acceptInput) (*sessionOutput, error) {
	if err := p.available(); err != nil {
		return nil, err
	}
	issued, err := p.auth.AcceptInvite(ctx, input.Body.Token, input.Body.Password, clientIP(ctx))
	if err != nil {
		return nil, authProblem(err)
	}
	return sessionResponse(issued), nil
}

func (p *panelAPI) requestReset(ctx context.Context, input *resetRequestInput) (*acceptedOutput, error) {
	if err := p.available(); err != nil {
		return nil, err
	}
	if err := p.auth.RequestPasswordReset(ctx, input.Body.Email, clientIP(ctx)); err != nil {
		return nil, authProblem(err)
	}
	return &acceptedOutput{Body: acceptedBody{Status: "accepted"}}, nil
}

func (p *panelAPI) confirmReset(ctx context.Context, input *resetConfirmInput) (*struct{}, error) {
	if err := p.available(); err != nil {
		return nil, err
	}
	if err := p.auth.ConfirmPasswordReset(ctx, input.Body.Token, input.Body.Password, clientIP(ctx)); err != nil {
		return nil, authProblem(err)
	}
	return &struct{}{}, nil
}

func (p *panelAPI) listSessions(ctx context.Context, input *cookieInput) (*sessionListOutput, error) {
	actor, err := p.readPanel(ctx, input.Session, input.CSRF, false)
	if err != nil {
		return nil, err
	}
	rows, err := p.auth.ListSessions(ctx, actor)
	if err != nil {
		return nil, authProblem(err)
	}
	out := &sessionListOutput{}
	out.Body.Sessions = make([]sessionInfoBody, 0, len(rows))
	for _, row := range rows {
		out.Body.Sessions = append(out.Body.Sessions, sessionInfoBody{
			ID:        row.ID,
			UserID:    row.UserID,
			CreatedAt: row.CreatedAt,
			ExpiresAt: row.ExpiresAt,
			Current:   row.Current,
		})
	}
	return out, nil
}

func (p *panelAPI) revoke(ctx context.Context, input *revokeInput) (*struct{}, error) {
	actor, err := p.readPanel(ctx, input.Session, input.CSRF, true)
	if err != nil {
		return nil, err
	}
	if err := p.auth.RevokeSession(ctx, actor, input.SessionID); err != nil {
		return nil, authProblem(err)
	}
	return &struct{}{}, nil
}

func (p *panelAPI) available() error {
	if p == nil || p.auth == nil {
		return huma.Error503ServiceUnavailable(auth.ErrUnavailable.Error())
	}
	return nil
}

func (p *panelAPI) read(ctx context.Context, cookie, csrf string, write bool) (auth.Principal, error) {
	if err := p.available(); err != nil {
		return auth.Principal{}, err
	}
	principal, err := p.auth.Authenticate(ctx, cookie, csrf, write)
	if err != nil {
		return auth.Principal{}, authProblem(err)
	}
	return principal, nil
}

func (p *panelAPI) readPanel(ctx context.Context, cookie, csrf string, write bool) (auth.Principal, error) {
	principal, err := p.read(ctx, cookie, csrf, write)
	if err != nil {
		return auth.Principal{}, err
	}
	if principal.EnrollmentOnly {
		return auth.Principal{}, authProblem(auth.ErrEnrollmentRequired)
	}
	return principal, nil
}

type enrollmentOutput struct {
	Body struct {
		OTPAuthURL    string   `json:"otpauth_url" doc:"URL otpauth para o aplicativo autenticador."`
		Secret        string   `json:"secret" doc:"Segredo base32, exibido uma vez."`
		RecoveryCodes []string `json:"recovery_codes" doc:"Códigos de recuperação, exibidos uma vez."`
	}
}

type confirmMFAInput struct {
	Session string `cookie:"__Host-session" doc:"Cookie de sessão do painel."`
	CSRF    string `header:"X-CSRF-Token" doc:"Token de CSRF vinculado à sessão."`
	Body    struct {
		Code string `json:"code" doc:"Código TOTP de seis dígitos." maxLength:"16"`
	}
}

type mfaStateOutput struct {
	Body struct {
		MFAEnrollmentRequired bool `json:"mfa_enrollment_required" doc:"Verdadeiro enquanto o MFA obrigatório não foi confirmado."`
	}
}

type resetMFAInput struct {
	Session string `cookie:"__Host-session" doc:"Cookie de sessão do painel."`
	CSRF    string `header:"X-CSRF-Token" doc:"Token de CSRF vinculado à sessão."`
	Body    struct {
		UserID uuid.UUID `json:"user_id" doc:"Usuário cujo MFA será apagado."`
	}
}

func (p *panelAPI) beginMFA(ctx context.Context, input *cookieInput) (*enrollmentOutput, error) {
	actor, err := p.read(ctx, input.Session, input.CSRF, true)
	if err != nil {
		return nil, err
	}
	created, err := p.auth.BeginEnrollment(ctx, actor)
	if err != nil {
		return nil, authProblem(err)
	}
	out := &enrollmentOutput{}
	out.Body.OTPAuthURL = created.OTPAuthURL
	out.Body.Secret = created.Secret
	out.Body.RecoveryCodes = created.RecoveryCodes
	return out, nil
}

func (p *panelAPI) confirmMFA(ctx context.Context, input *confirmMFAInput) (*mfaStateOutput, error) {
	actor, err := p.read(ctx, input.Session, input.CSRF, true)
	if err != nil {
		return nil, err
	}
	if err := p.auth.ConfirmEnrollment(ctx, actor, input.Body.Code); err != nil {
		return nil, authProblem(err)
	}
	out := &mfaStateOutput{}
	out.Body.MFAEnrollmentRequired = false
	return out, nil
}

func (p *panelAPI) resetMFA(ctx context.Context, input *resetMFAInput) (*struct{}, error) {
	actor, err := p.readPanel(ctx, input.Session, input.CSRF, true)
	if err != nil {
		return nil, err
	}
	if err := p.auth.ResetMFA(ctx, actor, input.Body.UserID); err != nil {
		return nil, authProblem(err)
	}
	return &struct{}{}, nil
}

func sessionResponse(issued auth.IssuedSession) *sessionOutput {
	return &sessionOutput{
		SetCookie: []string{
			auth.SessionSetCookie(issued.Token, issued.ExpiresAt),
			auth.CSRFSetCookie(issued.CSRFToken, issued.ExpiresAt),
		},
		Body: sessionBody{
			UserID:                issued.Principal.UserID,
			Email:                 issued.Principal.Email,
			TenantID:              issued.Principal.TenantID,
			MembershipID:          issued.Principal.MembershipID,
			Role:                  issued.Principal.Role,
			SessionID:             issued.Principal.SessionID,
			CSRFToken:             issued.CSRFToken,
			ExpiresAt:             issued.ExpiresAt,
			MFAEnrollmentRequired: issued.EnrollmentRequired,
		},
	}
}

func principalFrom(principal auth.Principal) principalBody {
	return principalBody{
		UserID:                principal.UserID,
		Email:                 principal.Email,
		TenantID:              principal.TenantID,
		MembershipID:          principal.MembershipID,
		Role:                  principal.Role,
		SessionID:             principal.SessionID,
		ExpiresAt:             principal.ExpiresAt,
		MFAEnrollmentRequired: principal.EnrollmentOnly,
	}
}

func clearSessionCookies() []string {
	return []string{auth.ClearSessionCookie(), auth.ClearCSRFCookie()}
}

func authProblem(err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrMFARequired), errors.Is(err, auth.ErrInvalidMFA):
		return huma.Error401Unauthorized(err.Error())
	case errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrEnrollmentRequired):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, auth.ErrLocked):
		return huma.Error429TooManyRequests(err.Error())
	case errors.Is(err, auth.ErrInvalidToken):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, auth.ErrInvalidPassword), errors.Is(err, auth.ErrInvalidEmail), errors.Is(err, auth.ErrInvalidRole):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, auth.ErrConflict), errors.Is(err, auth.ErrTenantRequired), errors.Is(err, auth.ErrMFAAlready):
		return huma.Error409Conflict(err.Error())
	default:
		return huma.Error503ServiceUnavailable(auth.ErrUnavailable.Error())
	}
}

func clientIP(ctx context.Context) string {
	value, _ := ctx.Value(clientIPKey{}).(string)
	if value == "" {
		return "invalid"
	}
	return value
}

func protectOrigins(trusted []string, deny http.Handler) func(http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	for _, origin := range trusted {
		_ = protection.AddTrustedOrigin(origin)
	}
	protection.SetDenyHandler(deny)
	return protection.Handler
}

func requestClientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	parsed := net.ParseIP(host)
	if parsed == nil {
		return "invalid"
	}
	return parsed.String()
}

func panelSafety(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/panel") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
		ctx := context.WithValue(r.Context(), clientIPKey{}, requestClientIP(r.RemoteAddr))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
