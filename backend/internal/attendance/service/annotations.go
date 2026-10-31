package service

import (
	"chatisp/internal/attendance/store"
	"chatisp/internal/sectors"
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s Service) authorizeAnnotationCatalog(ctx context.Context, tx pgx.Tx, p Principal) error {
	if err := s.authorizePrincipal(ctx, tx, p); err != nil {
		return err
	}
	if err := sectors.ValidatePrincipal(ctx, tx, sectorPrincipal(p)); errors.Is(err, sectors.ErrForbidden) {
		return ErrForbidden
	} else {
		return err
	}
}

// AnnotationCatalog lists the flat tenant tag catalogue for an active member.
func (s Service) AnnotationCatalog(ctx context.Context, p Principal) ([]store.AnnotationTag, error) {
	var out []store.AnnotationTag
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorizeAnnotationCatalog(ctx, tx, p); err != nil {
			return err
		}
		var err error
		out, err = store.AnnotationCatalog(ctx, tx, p.TenantID)
		return err
	})
	return out, err
}
func annotationManager(ctx context.Context, tx pgx.Tx, p Principal) error {
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE tenant_id=$1 AND id=$2 AND user_id=$3 AND status='active'`, p.TenantID, p.MembershipID, p.UserID).Scan(&role); err != nil {
		return ErrForbidden
	}
	if role != "admin" && role != "supervisor" {
		return ErrForbidden
	}
	return nil
}
func (s Service) CreateAnnotationTag(ctx context.Context, p Principal, name string) (store.AnnotationTag, error) {
	var out store.AnnotationTag
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorizeAnnotationCatalog(ctx, tx, p); err != nil {
			return err
		}
		if err := annotationManager(ctx, tx, p); err != nil {
			return err
		}
		var err error
		out, err = store.CreateAnnotationTag(ctx, tx, p.TenantID, name)
		return err
	})
	return out, err
}
func (s Service) ManageAnnotationTag(ctx context.Context, p Principal, id uuid.UUID, name string, remove bool) (store.AnnotationTag, error) {
	var out store.AnnotationTag
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorizeAnnotationCatalog(ctx, tx, p); err != nil {
			return err
		}
		if err := annotationManager(ctx, tx, p); err != nil {
			return err
		}
		var err error
		out, err = store.ManageAnnotationTag(ctx, tx, p.TenantID, p.MembershipID, id, name, remove)
		return err
	})
	return out, err
}
func (s Service) AnnotationRead(ctx context.Context, p Principal, inbox, conversation uuid.UUID) (store.AnnotationState, error) {
	var out store.AnnotationState
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inbox, false); err != nil {
			return err
		}
		if err := requireConversation(ctx, tx, p, inbox, conversation); err != nil {
			return err
		}
		var err error
		out, err = store.AnnotationRead(ctx, tx, p.TenantID, p.UserID, conversation)
		return err
	})
	return out, err
}
func (s Service) AnnotationChange(ctx context.Context, p Principal, inbox, conversation uuid.UUID, kind string, subject uuid.UUID, value, request string) (int64, error) {
	if kind != "note" && kind != "tag_add" && kind != "tag_remove" && kind != "priority" {
		return 0, store.ErrInvalid
	}
	var version int64
	err := s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inbox, false); err != nil {
			return err
		}
		if err := requireConversation(ctx, tx, p, inbox, conversation); err != nil {
			return err
		}
		if kind == "priority" {
			if err := annotationManager(ctx, tx, p); err != nil {
				return err
			}
		}
		var err error
		version, err = store.AnnotationChange(ctx, tx, p.TenantID, conversation, p.MembershipID, kind, subject, value, request)
		return err
	})
	return version, err
}
func (s Service) AnnotationMarkRead(ctx context.Context, p Principal, inbox, conversation uuid.UUID, watermark int64, unread bool, version int64) error {
	return s.tx(ctx, p.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.authorize(ctx, tx, p, inbox, false); err != nil {
			return err
		}
		if err := requireConversation(ctx, tx, p, inbox, conversation); err != nil {
			return err
		}
		return store.AnnotationMarkRead(ctx, tx, p.TenantID, p.UserID, conversation, watermark, unread, version)
	})
}
