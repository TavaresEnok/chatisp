package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"chatisp/internal/platform/auth"
	"chatisp/internal/platform/db"
)

func registerPlatform(api huma.API, deps Dependencies) {
	platform := &platformAPI{auth: deps.Auth}
	huma.Register(api, platformOp("list-platform-tenants", http.MethodGet, "/api/platform/tenants", "Lista provedores", "Superadmin. Só o cadastro do provedor, sem dados de atendimento.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable,
	}), platform.list)
	huma.Register(api, platformOp("create-platform-tenant", http.MethodPost, "/api/platform/tenants", "Cria um provedor", "Superadmin. A criação é auditada em platform_audit_logs.", http.StatusCreated, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusServiceUnavailable,
	}), platform.create)
	huma.Register(api, platformOp("get-platform-tenant", http.MethodGet, "/api/platform/tenants/{tenant_id}", "Mostra um provedor", "Superadmin. Não devolve conversas nem mensagens.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable,
	}), platform.get)
	huma.Register(api, platformOp("suspend-platform-tenant", http.MethodPost, "/api/platform/tenants/{tenant_id}/suspend", "Suspende um provedor", "Reversível. Não apaga dados. Derruba as sessões operacionais e grava auditoria de plataforma.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable,
	}), platform.suspend)
	huma.Register(api, platformOp("reactivate-platform-tenant", http.MethodPost, "/api/platform/tenants/{tenant_id}/reactivate", "Reativa um provedor", "Restaura o acesso operacional sem apagar nem recriar o histórico.", http.StatusOK, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable,
	}), platform.reactivate)
	huma.Register(api, platformOp("invite-platform-tenant-admin", http.MethodPost, "/api/platform/tenants/{tenant_id}/admin-invitations", "Convida o administrador de um provedor", "Superadmin. Envia por e-mail um convite de uso único com papel admin no provedor; o token não volta na resposta. Auditado em platform_audit_logs. Provedor suspenso responde 409.", http.StatusCreated, []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable,
	}), platform.inviteAdmin)
}

func platformOp(id, method, path, summary, description string, status int, errors []int) huma.Operation {
	return huma.Operation{
		OperationID:   id,
		Method:        method,
		Path:          path,
		Summary:       summary,
		Description:   description,
		Tags:          []string{"plataforma"},
		DefaultStatus: status,
		MaxBodyBytes:  4096,
		Errors:        errors,
	}
}

type platformAPI struct {
	auth *auth.Service
}

type limitsBody struct {
	MaxAgents       *int   `json:"max_agents,omitempty" doc:"Máximo de atendentes convidados ou ativos. Ausente não limita." minimum:"0"`
	MaxNumbers      *int   `json:"max_numbers,omitempty" doc:"Máximo de números. A aplicação do limite de canal fica nos marcos seguintes." minimum:"0"`
	MaxStorageBytes *int64 `json:"max_storage_bytes,omitempty" doc:"Máximo de armazenamento em bytes. A aplicação do limite de mídia fica nos marcos seguintes." minimum:"0"`
}

type tenantBody struct {
	ID        uuid.UUID  `json:"id" doc:"Provedor."`
	Timezone  string     `json:"timezone" doc:"Fuso do provedor."`
	Status    string     `json:"status" doc:"active ou suspended." enum:"active,suspended"`
	Limits    limitsBody `json:"limits" doc:"Limites do plano."`
	CreatedAt time.Time  `json:"created_at" doc:"Criação do provedor."`
	UpdatedAt time.Time  `json:"updated_at" doc:"Última mudança de cadastro."`
}

type tenantOutput struct {
	Body tenantBody
}

type tenantListOutput struct {
	Body struct {
		Tenants []tenantBody `json:"tenants" doc:"Cadastro dos provedores."`
	}
}

type tenantCreateInput struct {
	Session string `cookie:"__Host-session" doc:"Cookie de sessão do painel."`
	CSRF    string `header:"X-CSRF-Token" doc:"Token de CSRF vinculado à sessão."`
	Body    struct {
		Timezone string     `json:"timezone" doc:"Fuso IANA do provedor." minLength:"1" maxLength:"64"`
		Limits   limitsBody `json:"limits,omitempty" doc:"Limites do plano."`
	}
}

type tenantPathInput struct {
	Session  string    `cookie:"__Host-session" doc:"Cookie de sessão do painel."`
	CSRF     string    `header:"X-CSRF-Token" doc:"Token de CSRF vinculado à sessão."`
	TenantID uuid.UUID `path:"tenant_id" doc:"Provedor."`
}

type tenantAdminInviteInput struct {
	Session  string    `cookie:"__Host-session" doc:"Cookie de sessão do painel."`
	CSRF     string    `header:"X-CSRF-Token" doc:"Token de CSRF vinculado à sessão."`
	TenantID uuid.UUID `path:"tenant_id" doc:"Provedor."`
	Body     struct {
		Email string `json:"email" doc:"E-mail do administrador convidado." minLength:"3" maxLength:"320"`
	}
}

func (p *platformAPI) inviteAdmin(ctx context.Context, input *tenantAdminInviteInput) (*inviteOutput, error) {
	actor, err := p.read(ctx, input.Session, input.CSRF, true)
	if err != nil {
		return nil, err
	}
	created, err := p.auth.InviteProviderAdmin(ctx, actor, input.TenantID, input.Body.Email)
	if err != nil {
		return nil, authProblem(err)
	}
	return &inviteOutput{Body: inviteBody{ExpiresAt: created.ExpiresAt}}, nil
}

func (p *platformAPI) create(ctx context.Context, input *tenantCreateInput) (*tenantOutput, error) {
	actor, err := p.read(ctx, input.Session, input.CSRF, true)
	if err != nil {
		return nil, err
	}
	created, err := p.auth.CreateProvider(ctx, actor, input.Body.Timezone, planFromBody(input.Body.Limits))
	if err != nil {
		return nil, authProblem(err)
	}
	body, err := tenantFromDB(created)
	if err != nil {
		return nil, authProblem(auth.ErrUnavailable)
	}
	return &tenantOutput{Body: body}, nil
}

func (p *platformAPI) list(ctx context.Context, input *cookieInput) (*tenantListOutput, error) {
	actor, err := p.read(ctx, input.Session, input.CSRF, false)
	if err != nil {
		return nil, err
	}
	rows, err := p.auth.ListProviders(ctx, actor)
	if err != nil {
		return nil, authProblem(err)
	}
	out := &tenantListOutput{}
	out.Body.Tenants = make([]tenantBody, 0, len(rows))
	for _, row := range rows {
		body, err := tenantFromDB(row)
		if err != nil {
			return nil, authProblem(auth.ErrUnavailable)
		}
		out.Body.Tenants = append(out.Body.Tenants, body)
	}
	return out, nil
}

func (p *platformAPI) get(ctx context.Context, input *tenantPathInput) (*tenantOutput, error) {
	actor, err := p.read(ctx, input.Session, input.CSRF, false)
	if err != nil {
		return nil, err
	}
	row, err := p.auth.GetProvider(ctx, actor, input.TenantID)
	if err != nil {
		return nil, authProblem(err)
	}
	body, err := tenantFromDB(row)
	if err != nil {
		return nil, authProblem(auth.ErrUnavailable)
	}
	return &tenantOutput{Body: body}, nil
}

func (p *platformAPI) suspend(ctx context.Context, input *tenantPathInput) (*tenantOutput, error) {
	return p.change(ctx, input, true)
}

func (p *platformAPI) reactivate(ctx context.Context, input *tenantPathInput) (*tenantOutput, error) {
	return p.change(ctx, input, false)
}

func (p *platformAPI) change(ctx context.Context, input *tenantPathInput, suspend bool) (*tenantOutput, error) {
	actor, err := p.read(ctx, input.Session, input.CSRF, true)
	if err != nil {
		return nil, err
	}
	var row db.Tenant
	if suspend {
		row, err = p.auth.SuspendProvider(ctx, actor, input.TenantID)
	} else {
		row, err = p.auth.ReactivateProvider(ctx, actor, input.TenantID)
	}
	if err != nil {
		return nil, authProblem(err)
	}
	body, err := tenantFromDB(row)
	if err != nil {
		return nil, authProblem(auth.ErrUnavailable)
	}
	return &tenantOutput{Body: body}, nil
}

func (p *platformAPI) read(ctx context.Context, cookie, csrf string, write bool) (auth.Principal, error) {
	if p == nil || p.auth == nil {
		return auth.Principal{}, huma.Error503ServiceUnavailable(auth.ErrUnavailable.Error())
	}
	principal, err := p.auth.Authenticate(ctx, cookie, csrf, write)
	if err != nil {
		return auth.Principal{}, authProblem(err)
	}
	if principal.EnrollmentOnly {
		return auth.Principal{}, authProblem(auth.ErrEnrollmentRequired)
	}
	return principal, nil
}

func planFromBody(body limitsBody) auth.PlanLimits {
	return auth.PlanLimits{
		MaxAgents:       body.MaxAgents,
		MaxNumbers:      body.MaxNumbers,
		MaxStorageBytes: body.MaxStorageBytes,
	}
}

func tenantFromDB(row db.Tenant) (tenantBody, error) {
	var limits limitsBody
	if len(row.Limits) > 0 {
		if err := json.Unmarshal(row.Limits, &limits); err != nil {
			return tenantBody{}, err
		}
	}
	return tenantBody{
		ID:        row.ID,
		Timezone:  row.Timezone,
		Status:    row.Status,
		Limits:    limits,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}
