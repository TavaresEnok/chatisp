package configuration

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"chatisp/internal/attendance/store"
	"chatisp/internal/platform/crypto"
	"chatisp/internal/platform/db"
)

// Service configures Meta inboxes for one authenticated administrator.
// Tenant scope comes from the session row, never from the manual payload.
type Service struct {
	store   *db.Store
	ingress *pgxpool.Pool
	ring    *crypto.Ring
	now     func() time.Time
	log     *slog.Logger
}

// Server resolves registry data for ingress and dispatch.
// Callers inside this process construct it with Service.Server.
// An HTTP payload cannot obtain it.
type Server struct {
	svc *Service
}

// New binds the application store, the ingress pool and the master-key ring.
// The ring is the only cipher. This package does not implement AES.
func New(store *db.Store, ingress *pgxpool.Pool, ring *crypto.Ring) (*Service, error) {
	if store == nil || ingress == nil || ring == nil {
		return nil, errors.New("configuração de canal indisponível")
	}
	return &Service{store: store, ingress: ingress, ring: ring, now: time.Now}, nil
}

// SetLogger attaches a logger that receives identifiers only.
// Nil discards the operational lines. Secrets are not passed to it.
func (s *Service) SetLogger(log *slog.Logger) {
	if s != nil {
		s.log = log
	}
}

// Server returns the in-process resolver. It is not a panel operation.
func (s *Service) Server() *Server {
	if s == nil {
		return nil
	}
	return &Server{svc: s}
}

type actor struct {
	UserID       uuid.UUID
	TenantID     uuid.UUID
	MembershipID uuid.UUID
}

func (s *Service) authorize(ctx context.Context, sessionHash []byte) (actor, error) {
	if s == nil || len(sessionHash) != 32 {
		return actor{}, ErrUnauthenticated
	}
	var session db.SessionRecord
	var gate db.SessionGate
	err := s.store.WithAuth(ctx, func(ctx context.Context) error {
		var err error
		session, err = s.store.LoadSession(ctx, sessionHash, s.now())
		if err != nil {
			return err
		}
		gate, err = s.store.SessionGate(ctx, sessionHash)
		return err
	})
	if err != nil {
		return actor{}, ErrUnauthenticated
	}
	if session.Status != "active" || session.TenantID == uuid.Nil || session.Role == "" {
		return actor{}, ErrUnauthenticated
	}
	if session.Role != "admin" {
		return actor{}, ErrForbidden
	}
	if !gate.Found || gate.Assurance != "full" || !gate.Confirmed {
		return actor{}, ErrMFARequired
	}
	return actor{UserID: session.UserID, TenantID: session.TenantID, MembershipID: session.MembershipID}, nil
}

func (s *Service) note(ctx context.Context, msg string, connector uuid.UUID, version int64) {
	if s.log == nil {
		return
	}
	s.log.InfoContext(ctx, msg, "connector_id", connector, "version", version)
}

func mapWriteErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrInvalid) || errors.Is(err, ErrInvalid) {
		return ErrInvalid
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, store.ErrTenantContextRequired) || errors.Is(err, store.ErrIngressContextRequired) {
		return ErrUnauthenticated
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrSecret) || errors.Is(err, ErrStaleVersion) || errors.Is(err, ErrDegraded) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23514", "23503", "22023":
			return ErrInvalid
		case "42501":
			return ErrForbidden
		}
	}
	return ErrUnavailable
}
