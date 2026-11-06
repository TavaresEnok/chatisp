package db

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MFARecord is the stored TOTP material. Ciphertext is empty when MFA is not enrolled.
// The master key that opens Ciphertext is not in the database.
type MFARecord struct {
	Ciphertext   []byte
	KeyVersion   string
	AADTenant    uuid.UUID
	Confirmed    bool
	IsSuperadmin bool
}

// RecoveryCode is the hash of one unused recovery code.
type RecoveryCode struct {
	ID   uuid.UUID
	Hash []byte
}

// SessionGate is the MFA state of a session, loaded with its cookie hash.
type SessionGate struct {
	Assurance    string
	Confirmed    bool
	IsSuperadmin bool
	Found        bool
}

// SessionGate loads MFA assurance for a session hash. It requires WithAuth.
func (s *Store) SessionGate(ctx context.Context, tokenHash []byte) (SessionGate, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return SessionGate{}, err
	}
	rows, err := scope.tx.Query(ctx, `
		SELECT assurance, totp_confirmed, is_superadmin
		FROM auth_session_gate($1)
	`, tokenHash)
	if err != nil {
		return SessionGate{}, mapAuthErr(err)
	}
	defer rows.Close()
	var gate SessionGate
	if rows.Next() {
		if err := rows.Scan(&gate.Assurance, &gate.Confirmed, &gate.IsSuperadmin); err != nil {
			return SessionGate{}, mapAuthErr(err)
		}
		gate.Found = true
	}
	if err := rows.Err(); err != nil {
		return SessionGate{}, mapAuthErr(err)
	}
	return gate, nil
}

// SetSessionAssurance marks a live session as full or enrollment-only. It requires WithAuth.
func (s *Store) SetSessionAssurance(ctx context.Context, id uuid.UUID, assurance string) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `SELECT auth_set_session_assurance($1, $2)`, id, assurance)
	return mapAuthErr(err)
}

// MFARecord loads the encrypted TOTP secret. It requires WithAuth.
func (s *Store) MFARecord(ctx context.Context, userID uuid.UUID) (MFARecord, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return MFARecord{}, err
	}
	var record MFARecord
	var tenant *uuid.UUID
	err = scope.tx.QueryRow(ctx, `
		SELECT totp_ciphertext, totp_key_version, totp_aad_tenant, totp_confirmed, is_superadmin
		FROM auth_mfa_record($1)
	`, userID).Scan(&record.Ciphertext, &record.KeyVersion, &tenant, &record.Confirmed, &record.IsSuperadmin)
	if err != nil {
		return MFARecord{}, mapAuthErr(err)
	}
	if tenant != nil {
		record.AADTenant = *tenant
	}
	return record, nil
}

// BeginMFA stores a new ciphertext and drops previous recovery codes. It requires WithAuth.
func (s *Store) BeginMFA(ctx context.Context, userID, tenantID, sessionID uuid.UUID, ciphertext []byte, version string, now time.Time) (bool, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return false, err
	}
	var ok bool
	err = scope.tx.QueryRow(ctx, `SELECT auth_mfa_begin_optional($1, $2, $3, $4, $5, $6)`, userID, ciphertext, version, tenantID, sessionID, now).Scan(&ok)
	return ok, mapAuthErr(err)
}

// AddRecoveryCode stores one hashed recovery code. It requires WithAuth.
func (s *Store) AddRecoveryCode(ctx context.Context, id, userID uuid.UUID, codeHash []byte, now time.Time) error {
	scope, err := authFrom(ctx)
	if err != nil {
		return err
	}
	_, err = scope.tx.Exec(ctx, `SELECT auth_mfa_add_code($1, $2, $3, $4)`, id, userID, codeHash, now)
	return mapAuthErr(err)
}

// ConfirmMFA marks the stored secret as confirmed. It requires WithAuth.
func (s *Store) ConfirmMFA(ctx context.Context, userID, sessionID uuid.UUID, ciphertext []byte, now time.Time) (bool, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return false, err
	}
	var ok bool
	err = scope.tx.QueryRow(ctx, `SELECT auth_mfa_confirm_optional($1, $2, $3, $4)`, userID, sessionID, ciphertext, now).Scan(&ok)
	if err != nil {
		return false, mapAuthErr(err)
	}
	return ok, nil
}

// ResetMFA revalidates the actor and revokes target sessions atomically.
func (s *Store) ResetMFA(ctx context.Context, actor, session, tenant, target uuid.UUID, now time.Time) (bool, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return false, err
	}
	var ok bool
	err = scope.tx.QueryRow(ctx, `SELECT auth_mfa_reset_optional($1, $2, $3, $4, $5)`, actor, session, tenant, target, now).Scan(&ok)
	return ok, mapAuthErr(err)
}

// ListRecoveryCodes returns unused recovery hashes. It requires WithAuth.
func (s *Store) ListRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]RecoveryCode, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := scope.tx.Query(ctx, `SELECT id, code_hash FROM auth_mfa_list_codes($1)`, userID)
	if err != nil {
		return nil, mapAuthErr(err)
	}
	defer rows.Close()
	var out []RecoveryCode
	for rows.Next() {
		var row RecoveryCode
		if err := rows.Scan(&row.ID, &row.Hash); err != nil {
			return nil, mapAuthErr(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mapAuthErr(err)
	}
	return out, nil
}

// ConsumeRecoveryCode marks one unused code as used. It requires WithAuth.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID, id uuid.UUID, now time.Time) (bool, error) {
	scope, err := authFrom(ctx)
	if err != nil {
		return false, err
	}
	var ok bool
	err = scope.tx.QueryRow(ctx, `SELECT auth_mfa_consume_code($1, $2, $3)`, userID, id, now).Scan(&ok)
	if err != nil {
		return false, mapAuthErr(err)
	}
	return ok, nil
}
