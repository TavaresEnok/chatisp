package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/attendance/store"
	"chatisp/internal/channel/configuration"
	"chatisp/internal/ingress/authmeta"
	"chatisp/internal/platform/db"
	"chatisp/internal/platform/jobs"
)

// WebhookConfig contains parameters required for configuring the Meta webhook receiver.
type WebhookConfig struct {
	AppID        string
	AppResolver  AppResolver
	VerifyTokens VerifyTokenProvider
	IngressPool  *pgxpool.Pool
	JobsClient   *jobs.Client
	Logger       *slog.Logger
	MaxBodyBytes int64
}

// WebhookHandler handles incoming Meta Cloud webhooks (both GET verification and POST delivery).
// It resolves the trusted App before evaluating HMAC and persists raw envelopes atomically
// with River ingress jobs before returning HTTP 200.
type WebhookHandler struct {
	cfg  WebhookConfig
	repo store.Repo
}

// NewWebhookHandler creates an HTTP handler for Meta Cloud webhooks.
func NewWebhookHandler(cfg WebhookConfig) (*WebhookHandler, error) {
	if cfg.AppResolver == nil || cfg.VerifyTokens == nil || cfg.IngressPool == nil || cfg.JobsClient == nil {
		return nil, ErrInvalid
	}
	return &WebhookHandler{
		cfg:  cfg,
		repo: store.Repo{},
	}, nil
}

// ServeHTTP handles the webhook verification or event delivery.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.cfg.AppResolver == nil {
		http.Error(w, "pipeline indisponível", http.StatusInternalServerError)
		return
	}
	appID := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if appID == "" {
		appID = strings.TrimSpace(r.Header.Get("X-Meta-App-ID"))
	}
	if appID == "" {
		appID = strings.TrimSpace(h.cfg.AppID)
	}
	if appID == "" {
		http.Error(w, "app_id não identificado", http.StatusBadRequest)
		return
	}

	// 1. Resolve trusted App BEFORE HMAC or challenge processing.
	trusted, err := h.cfg.AppResolver.ResolveApp(r.Context(), appID)
	if err != nil {
		if errors.Is(err, configuration.ErrNotFound) || errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrNotFound) {
			http.Error(w, "aplicativo não encontrado", http.StatusUnauthorized)
			return
		}
		http.Error(w, "falha ao resolver aplicativo", http.StatusInternalServerError)
		return
	}

	// 2. Resolve distinct verification token.
	verifyToken, err := h.cfg.VerifyTokens.VerifyToken(r.Context(), appID)
	if err != nil || verifyToken == "" || verifyToken == trusted.AppSecret {
		http.Error(w, "token de verificação inválido", http.StatusUnauthorized)
		return
	}

	// 3. Configure trusted Meta app binding.
	app := authmeta.App{
		ID:                trusted.AppID,
		Secret:            trusted.AppSecret,
		VerificationToken: verifyToken,
		MaxBodyBytes:      h.cfg.MaxBodyBytes,
	}

	// 4. Build atomic persister callback.
	persister := authmeta.PersistFunc(func(ctx context.Context, env authmeta.Envelope) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return db.WithIngressTx(ctx, h.cfg.IngressPool, func(ctx context.Context, tx pgx.Tx) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			del, err := h.repo.StoreDelivery(ctx, tx, env.AppID, env.Raw, "", "")
			if err != nil {
				return err
			}
			if !del.Replayed {
				if _, err := h.cfg.JobsClient.EnqueueIngress(ctx, tx, del.ID); err != nil {
					return err
				}
			}
			return nil
		})
	})

	// 5. Delegate to authmeta handler.
	authHandler := authmeta.New(app, persister, h.cfg.Logger)
	authHandler.ServeHTTP(w, r)
}
