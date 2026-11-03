package presence

import (
	"context"
	"errors"
	"time"

	"chatisp/internal/sectors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Lock order inside a tenant transaction: shared or exclusive tenant advisory,
// then the caller's membership, then presence_settings, then presence_operator,
// then presence_session_live (users before sessions), then connection rows.
// Call Effective and ListAvailable before exclusive conversation locks.

func normalize(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Microsecond)
}

func pinScope(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	if tx == nil || tenant == uuid.Nil {
		return ErrInvalid
	}
	var scope string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.tenant_id', true), '')`).Scan(&scope); err != nil {
		return err
	}
	if scope != tenant.String() {
		return ErrForbidden
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext('chatisp-tenant'), hashtext($1::text))`, tenant.String()); err != nil {
		return err
	}
	return nil
}

func tenantActive(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1`, tenant).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || status != "active" {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return ErrForbidden
	}
	return err
}

func pinSettings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Settings, error) {
	out := Settings{OfflineToleranceSeconds: int(DefaultOfflineTolerance / time.Second)}
	err := tx.QueryRow(ctx, `SELECT version, offline_tolerance_seconds FROM presence_settings WHERE tenant_id=$1 FOR SHARE`, tenant).Scan(&out.Version, &out.OfflineToleranceSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return Settings{}, err
	}
	out.Configured = true
	return out, nil
}

type operatorRow struct {
	Member   uuid.UUID
	Role     string
	Status   string
	Manual   string
	Explicit *time.Time
	Found    bool
}

func loadMember(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (operatorRow, error) {
	var row operatorRow
	err := tx.QueryRow(ctx, `SELECT id, role, status FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE`, tenant, user).Scan(&row.Member, &row.Role, &row.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, nil
	}
	if err != nil {
		return row, err
	}
	row.Found = true
	row.Manual = ManualAvailable
	return row, nil
}

func loadOperator(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, row *operatorRow) error {
	err := tx.QueryRow(ctx, `SELECT manual, explicit_offline_at FROM presence_operator WHERE tenant_id=$1 AND user_id=$2 FOR SHARE`, tenant, user).Scan(&row.Manual, &row.Explicit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func sessionLive(ctx context.Context, tx pgx.Tx, session, tenant, member, user uuid.UUID, now time.Time) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT presence_session_live($1,$2,$3,$4,$5)`, session, tenant, member, user, now).Scan(&ok)
	return ok, err
}

func ensureOperator(ctx context.Context, tx pgx.Tx, tenant, user, member uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO presence_operator(tenant_id,user_id,membership_id,manual,version) VALUES($1,$2,$3,'available',1) ON CONFLICT(tenant_id,user_id) DO UPDATE SET membership_id=EXCLUDED.membership_id`, tenant, user, member)
	return err
}

// Effective aggregates one user inside an existing tenant transaction.
// A suspended tenant returns ErrForbidden. A disabled user or membership is offline.
// The snapshot does not assign or release conversations.
func Effective(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, now time.Time) (Snapshot, error) {
	var out Snapshot
	now = normalize(now)
	if user == uuid.Nil || now.IsZero() {
		return out, ErrInvalid
	}
	if err := pinScope(ctx, tx, tenant); err != nil {
		return out, err
	}
	if err := tenantActive(ctx, tx, tenant); err != nil {
		return out, err
	}
	member, err := loadMember(ctx, tx, tenant, user)
	if err != nil {
		return out, err
	}
	settings, err := pinSettings(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	if err = loadOperator(ctx, tx, tenant, user, &member); err != nil {
		return out, err
	}
	out = Snapshot{
		UserID: user, MembershipID: member.Member, State: StateOffline, Manual: ManualAvailable,
		ToleranceSeconds: settings.OfflineToleranceSeconds,
	}
	if member.Found {
		out.Manual = member.Manual
	}
	type tab struct {
		session, member uuid.UUID
		heartbeat, exp  time.Time
	}
	rows, err := tx.Query(ctx, `SELECT session_id, membership_id, heartbeat_at, expires_at FROM presence_connections WHERE tenant_id=$1 AND user_id=$2 ORDER BY session_id, tab_id`, tenant, user)
	if err != nil {
		return Snapshot{}, err
	}
	var tabs []tab
	for rows.Next() {
		var item tab
		if err = rows.Scan(&item.session, &item.member, &item.heartbeat, &item.exp); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		tabs = append(tabs, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Snapshot{}, err
	}
	rows.Close()
	seen := map[uuid.UUID]bool{}
	var sessions []tab
	for _, item := range tabs {
		if !seen[item.session] {
			seen[item.session] = true
			sessions = append(sessions, item)
		}
	}
	liveSession := map[uuid.UUID]bool{}
	for _, item := range sessions {
		ok, err := sessionLive(ctx, tx, item.session, tenant, item.member, user, now)
		if err != nil {
			return Snapshot{}, err
		}
		liveSession[item.session] = ok
	}
	if len(tabs) > 0 {
		locked, err := tx.Query(ctx, `SELECT 1 FROM presence_connections WHERE tenant_id=$1 AND user_id=$2 ORDER BY session_id, tab_id FOR SHARE`, tenant, user)
		if err != nil {
			return Snapshot{}, err
		}
		for locked.Next() {
		}
		err = locked.Err()
		locked.Close()
		if err != nil {
			return Snapshot{}, err
		}
	}
	var lastHB, lastExp time.Time
	var have bool
	for _, item := range tabs {
		hb, exp := item.heartbeat.UTC(), item.exp.UTC()
		if !have || hb.After(lastHB) {
			lastHB = hb
		}
		if !have || exp.After(lastExp) {
			lastExp = exp
		}
		have = true
		if liveSession[item.session] && exp.After(now) {
			out.TabCount++
		}
	}
	out.Connected = out.TabCount > 0
	if have {
		hb, exp := lastHB, lastExp
		out.LastHeartbeat = &hb
		out.ConnectedUntil = &exp
	}
	actorOK := member.Found && member.Status == "active" && (member.Role == "admin" || member.Role == "supervisor" || member.Role == "agent")
	if out.Connected && !actorOK {
		out.Connected = false
		out.TabCount = 0
	}
	// A disabled user can still own a session row that the gate rejects. Do not
	// treat those tabs as a connection, including for the manual busy state.
	if out.Connected && actorOK && out.Manual == ManualBusy {
		out.State = StateBusy
	} else if out.Connected && actorOK {
		out.State = StateAvailable
	}
	if !out.Connected {
		var since *time.Time
		if member.Explicit != nil && !member.Explicit.After(now) {
			t := member.Explicit.UTC()
			since = &t
		} else if have && !lastExp.After(now) {
			t := lastExp
			since = &t
		}
		out.OfflineSince = since
		if since != nil && !now.Before(since.Add(time.Duration(out.ToleranceSeconds)*time.Second)) {
			out.OfflineForUnassign = true
		}
	}
	out.Assignable = out.State == StateAvailable
	return out, nil
}

// ListAvailable returns attendants of one active sector who can receive a new
// conversation: live tab, manual state available, active membership and user.
// Busy and offline are omitted. Capacity is not consulted. An inactive or
// missing sector yields an empty list. A suspended tenant is forbidden.
func ListAvailable(ctx context.Context, tx pgx.Tx, tenant, sector uuid.UUID, now time.Time) ([]Snapshot, error) {
	if sector == uuid.Nil || normalize(now).IsZero() {
		return nil, ErrInvalid
	}
	if err := pinScope(ctx, tx, tenant); err != nil {
		return nil, err
	}
	if err := tenantActive(ctx, tx, tenant); err != nil {
		return nil, err
	}
	var active bool
	err := tx.QueryRow(ctx, `SELECT active FROM sectors WHERE tenant_id=$1 AND id=$2 FOR SHARE`, tenant, sector).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return []Snapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT m.user_id, sm.membership_id FROM sector_memberships sm JOIN memberships m ON m.tenant_id=sm.tenant_id AND m.id=sm.membership_id AND m.status='active' AND m.role IN ('admin','supervisor','agent') WHERE sm.tenant_id=$1 AND sm.sector_id=$2 ORDER BY m.user_id`, tenant, sector)
	if err != nil {
		return nil, err
	}
	var users []uuid.UUID
	var members []uuid.UUID
	for rows.Next() {
		var user, member uuid.UUID
		if err = rows.Scan(&user, &member); err != nil {
			rows.Close()
			return nil, err
		}
		users = append(users, user)
		members = append(members, member)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range users {
		locked, err := tx.Query(ctx, `SELECT 1 FROM sector_memberships WHERE tenant_id=$1 AND sector_id=$2 AND membership_id=$3 FOR SHARE`, tenant, sector, members[i])
		if err != nil {
			return nil, err
		}
		for locked.Next() {
		}
		err = locked.Err()
		locked.Close()
		if err != nil {
			return nil, err
		}
	}
	out := []Snapshot{}
	for _, user := range users {
		snap, err := Effective(ctx, tx, tenant, user, now)
		if err != nil {
			return nil, err
		}
		if snap.Assignable {
			out = append(out, snap)
		}
	}
	return out, nil
}

func sectorPrincipal(p Principal) sectors.Principal {
	return sectors.Principal{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, Role: p.Role, IsSuperadmin: p.IsSuperadmin}
}
