package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
)

// pgStore is the PostgreSQL adapter of Store. It is the only file of the package that may import
// pgx (depguard rule pgx-only-in-store-pg); everything it returns is a plain Go type.
//
// Reads and plain writes use the transaction in ctx when there is one and the pool otherwise;
// the methods documented "requires a transaction" go through db.MustTx and return db.ErrNoTx.
type pgStore struct{ txr db.TxRunner }

// NewPGStore returns the PostgreSQL-backed Store.
func NewPGStore(txr db.TxRunner) Store { return &pgStore{txr: txr} }

var _ Store = (*pgStore)(nil)

// Constraint names of 00003_auth.sql that carry a domain meaning.
const (
	constraintUserEmail      = "users_email_key"
	constraintUserMobile     = "users_mobile_key"
	constraintLiveDevice     = "device_sessions_live_device_key"
	constraintRefreshHash    = "refresh_tokens_hash_key"
	constraintRefreshOneLive = "refresh_tokens_one_live_key"
)

// q runs on the transaction in ctx, or the pool.
func (s *pgStore) q(ctx context.Context) *dbgen.Queries { return dbgen.New(s.txr.Conn(ctx)) }

// qTx runs on the transaction in ctx and returns db.ErrNoTx without one.
func (s *pgStore) qTx(ctx context.Context) (*dbgen.Queries, error) {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return dbgen.New(tx), nil
}

// uniqueViolation returns the constraint name when err is a PostgreSQL unique violation.
func uniqueViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func nullText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func nullUUIDValue(u uuid.NullUUID) uuid.UUID { return u.UUID } // uuid.Nil when NULL

// ---- organisation ----

func (s *pgStore) DefaultOrg(ctx context.Context) (uuid.UUID, error) {
	ids, err := s.q(ctx).ListFirstTwoOrgIDs(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("auth: list orgs: %w", err)
	}
	switch len(ids) {
	case 0:
		return uuid.Nil, ErrNoOrg
	case 1:
		return ids[0], nil
	default:
		return uuid.Nil, ErrMultipleOrgs
	}
}

// ---- users ----

func userRow(u dbgen.User) UserRow {
	return UserRow{
		ID: u.ID, OrgID: u.OrgID, Role: u.Role, Status: Status(u.Status),
		StatusBeforeDisabled: Status(u.StatusBeforeDisabled.String),
		Name:                 u.Name, Email: u.Email, Mobile: u.Mobile.String,
		EmailVerifiedAt: db.TimePtr(u.EmailVerifiedAt), MobileVerifiedAt: db.TimePtr(u.MobileVerifiedAt),
		PasswordHash: u.PasswordHash, PasswordChangedAt: db.Time(u.PasswordChangedAt),
		LastSignInAt: db.TimePtr(u.LastSignInAt),
		CreatedAt:    db.Time(u.CreatedAt), UpdatedAt: db.Time(u.UpdatedAt),
	}
}

func (s *pgStore) CreateUser(ctx context.Context, in NewUser) (UserRow, error) {
	u, err := s.q(ctx).InsertUser(ctx, dbgen.InsertUserParams{
		OrgID: in.OrgID, Role: in.Role, Status: string(in.Status), Name: in.Name, Email: in.Email,
		Mobile: nullText(in.Mobile), EmailVerifiedAt: db.NullTimestamptz(in.EmailVerifiedAt),
		PasswordHash: in.PasswordHash, Now: db.Timestamptz(in.Now),
	})
	if err != nil {
		switch name, _ := uniqueViolation(err); name {
		case constraintUserEmail:
			return UserRow{}, ErrEmailTaken
		case constraintUserMobile:
			return UserRow{}, ErrMobileTaken
		}
		return UserRow{}, fmt.Errorf("auth: create user: %w", err)
	}
	return userRow(u), nil
}

func (s *pgStore) GetUser(ctx context.Context, id uuid.UUID) (UserRow, error) {
	u, err := s.q(ctx).GetUserByID(ctx, id)
	return oneUser(u, err)
}

func (s *pgStore) FindUserByIdentifier(ctx context.Context, kind IdentifierKind, value string) (UserRow, error) {
	var u dbgen.User
	var err error
	switch kind {
	case KindEmail:
		u, err = s.q(ctx).GetUserByEmail(ctx, value)
	case KindMobile:
		u, err = s.q(ctx).GetUserByMobile(ctx, nullText(value))
	default:
		return UserRow{}, ErrInvalidIdentifier
	}
	return oneUser(u, err)
}

func (s *pgStore) LockUser(ctx context.Context, id uuid.UUID) (UserRow, error) {
	q, err := s.qTx(ctx)
	if err != nil {
		return UserRow{}, err
	}
	u, err := q.LockUserByID(ctx, id)
	return oneUser(u, err)
}

func oneUser(u dbgen.User, err error) (UserRow, error) {
	if notFound(err) {
		return UserRow{}, ErrUserNotFound
	}
	if err != nil {
		return UserRow{}, fmt.Errorf("auth: read user: %w", err)
	}
	return userRow(u), nil
}

func (s *pgStore) LockAdmins(ctx context.Context, orgID uuid.UUID) ([]UserRow, error) {
	q, err := s.qTx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.LockAdminsByOrg(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("auth: lock admins: %w", err)
	}
	out := make([]UserRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, userRow(r))
	}
	return out, nil
}

// userWritten maps "no row changed" of an UPDATE by id to ErrUserNotFound.
func userWritten(rows int64, err error, what string) error {
	if err != nil {
		return fmt.Errorf("auth: %s: %w", what, err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *pgStore) SetStatus(ctx context.Context, id uuid.UUID, to, before Status, now time.Time) error {
	rows, err := s.q(ctx).SetUserStatus(ctx, dbgen.SetUserStatusParams{
		ID: id, Status: string(to), StatusBeforeDisabled: nullText(string(before)), Now: db.Timestamptz(now),
	})
	return userWritten(rows, err, "set status")
}

func (s *pgStore) MarkVerified(ctx context.Context, id uuid.UUID, kind IdentifierKind, now time.Time) (bool, error) {
	var rows int64
	var err error
	switch kind {
	case KindEmail:
		rows, err = s.q(ctx).MarkEmailVerified(ctx, dbgen.MarkEmailVerifiedParams{ID: id, Now: db.Timestamptz(now)})
	case KindMobile:
		rows, err = s.q(ctx).MarkMobileVerified(ctx, dbgen.MarkMobileVerifiedParams{ID: id, Now: db.Timestamptz(now)})
	default:
		return false, ErrInvalidIdentifier
	}
	if err != nil {
		return false, fmt.Errorf("auth: mark verified: %w", err)
	}
	return rows > 0, nil
}

func (s *pgStore) ReplaceContact(ctx context.Context, id uuid.UUID, kind IdentifierKind, value string, now time.Time) error {
	var rows int64
	var err error
	switch kind {
	case KindEmail:
		rows, err = s.q(ctx).ReplaceUserEmail(ctx, dbgen.ReplaceUserEmailParams{ID: id, Email: value, Now: db.Timestamptz(now)})
	case KindMobile:
		rows, err = s.q(ctx).ReplaceUserMobile(ctx, dbgen.ReplaceUserMobileParams{ID: id, Mobile: nullText(value), Now: db.Timestamptz(now)})
	default:
		return ErrInvalidIdentifier
	}
	if err != nil {
		switch name, _ := uniqueViolation(err); name {
		case constraintUserEmail:
			return ErrEmailTaken
		case constraintUserMobile:
			return ErrMobileTaken
		}
	}
	return userWritten(rows, err, "replace contact")
}

func (s *pgStore) ChangePassword(ctx context.Context, id uuid.UUID, phc string, now time.Time) error {
	rows, err := s.q(ctx).SetUserPassword(ctx, dbgen.SetUserPasswordParams{ID: id, PasswordHash: phc, Now: db.Timestamptz(now)})
	return userWritten(rows, err, "change password")
}

func (s *pgStore) Rehash(ctx context.Context, id uuid.UUID, oldPHC, newPHC string, now time.Time) (bool, error) {
	rows, err := s.q(ctx).RehashUserPassword(ctx, dbgen.RehashUserPasswordParams{
		ID: id, OldHash: oldPHC, NewHash: newPHC, Now: db.Timestamptz(now),
	})
	if err != nil {
		return false, fmt.Errorf("auth: rehash: %w", err)
	}
	return rows > 0, nil
}

func (s *pgStore) RecordSignIn(ctx context.Context, id uuid.UUID, now time.Time) error {
	rows, err := s.q(ctx).SetUserLastSignIn(ctx, dbgen.SetUserLastSignInParams{ID: id, Now: db.Timestamptz(now)})
	return userWritten(rows, err, "record sign-in")
}

// ---- sessions ----

func sessionRow(d dbgen.DeviceSession) SessionRow {
	return SessionRow{
		ID: d.ID, OrgID: d.OrgID, UserID: d.UserID, DeviceID: d.DeviceID, DeviceName: d.DeviceName,
		Platform: d.Platform, ClientType: d.ClientType, MFAMethod: d.MfaMethod.String,
		CreatedAt: db.Time(d.CreatedAt), LastActiveAt: db.Time(d.LastActiveAt),
		IdleExpiresAt: db.Time(d.IdleExpiresAt), AbsoluteExpiresAt: db.Time(d.AbsoluteExpiresAt),
		EndedAt: db.TimePtr(d.EndedAt), EndReason: d.EndReason.String,
	}
}

func oneSession(d dbgen.DeviceSession, err error) (SessionRow, error) {
	if notFound(err) {
		return SessionRow{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionRow{}, fmt.Errorf("auth: read session: %w", err)
	}
	return sessionRow(d), nil
}

func (s *pgStore) CreateSession(ctx context.Context, in NewSession) (SessionRow, error) {
	d, err := s.q(ctx).InsertSession(ctx, dbgen.InsertSessionParams{
		OrgID: in.OrgID, UserID: in.UserID, DeviceID: in.DeviceID, DeviceName: in.DeviceName,
		Platform: in.Platform, ClientType: in.ClientType, MfaMethod: nullText(in.MFAMethod),
		Now: db.Timestamptz(in.Now), IdleExpiresAt: db.Timestamptz(in.IdleExpiresAt),
		AbsoluteExpiresAt: db.Timestamptz(in.AbsoluteExpiresAt),
	})
	if err != nil {
		if name, ok := uniqueViolation(err); ok && name == constraintLiveDevice {
			return SessionRow{}, ErrDeviceSessionExists
		}
		return SessionRow{}, fmt.Errorf("auth: create session: %w", err)
	}
	return sessionRow(d), nil
}

func (s *pgStore) GetSession(ctx context.Context, id uuid.UUID) (SessionRow, error) {
	d, err := s.q(ctx).GetSessionByID(ctx, id)
	return oneSession(d, err)
}

func (s *pgStore) LockSession(ctx context.Context, id uuid.UUID) (SessionRow, error) {
	q, err := s.qTx(ctx)
	if err != nil {
		return SessionRow{}, err
	}
	d, err := q.LockSessionByID(ctx, id)
	return oneSession(d, err)
}

func (s *pgStore) GetSessionPrincipal(ctx context.Context, sessionID uuid.UUID) (SessionPrincipal, error) {
	r, err := s.q(ctx).GetSessionPrincipal(ctx, sessionID)
	if notFound(err) {
		return SessionPrincipal{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionPrincipal{}, fmt.Errorf("auth: read session principal: %w", err)
	}
	return SessionPrincipal{
		SessionID: r.SessionID, UserID: r.UserID, OrgID: r.OrgID, EndedAt: db.TimePtr(r.EndedAt),
		IdleExpiresAt: db.Time(r.IdleExpiresAt), AbsoluteExpiresAt: db.Time(r.AbsoluteExpiresAt),
		Role: r.Role, Status: Status(r.Status),
	}, nil
}

func (s *pgStore) ListLiveSessions(ctx context.Context, userID uuid.UUID, now time.Time) ([]SessionRow, error) {
	rows, err := s.q(ctx).ListLiveSessionsByUser(ctx, dbgen.ListLiveSessionsByUserParams{UserID: userID, Now: db.Timestamptz(now)})
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	out := make([]SessionRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, sessionRow(r))
	}
	return out, nil
}

func (s *pgStore) TouchSession(ctx context.Context, id uuid.UUID, now, idleExpiresAt time.Time) error {
	rows, err := s.q(ctx).TouchSession(ctx, dbgen.TouchSessionParams{ID: id, Now: db.Timestamptz(now), IdleExpiresAt: db.Timestamptz(idleExpiresAt)})
	if err != nil {
		return fmt.Errorf("auth: touch session: %w", err)
	}
	if rows == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (s *pgStore) EndSession(ctx context.Context, id uuid.UUID, reason string, now time.Time) (bool, error) {
	rows, err := s.q(ctx).EndSession(ctx, dbgen.EndSessionParams{ID: id, EndReason: db.Text(reason), Now: db.Timestamptz(now)})
	if err != nil {
		return false, fmt.Errorf("auth: end session: %w", err)
	}
	return rows > 0, nil
}

func (s *pgStore) EndUserSessions(ctx context.Context, userID uuid.UUID, exceptSessionID uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error) {
	ids, err := s.q(ctx).EndUserSessions(ctx, dbgen.EndUserSessionsParams{
		UserID: userID, ExceptSessionID: exceptSessionID, EndReason: db.Text(reason), Now: db.Timestamptz(now),
	})
	if err != nil {
		return nil, fmt.Errorf("auth: end user sessions: %w", err)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return ids, nil
}

// ---- refresh tokens ----

func refreshRow(r dbgen.RefreshToken) RefreshRow {
	return RefreshRow{
		ID: r.ID, OrgID: r.OrgID, SessionID: r.SessionID, TokenHash: r.TokenHash,
		IssuedAt: db.Time(r.IssuedAt), UsedAt: db.TimePtr(r.UsedAt), ReplacedBy: nullUUIDValue(r.ReplacedBy),
	}
}

func oneRefresh(r dbgen.RefreshToken, err error) (RefreshRow, error) {
	if notFound(err) {
		return RefreshRow{}, ErrRefreshNotFound
	}
	if err != nil {
		return RefreshRow{}, fmt.Errorf("auth: read refresh token: %w", err)
	}
	return refreshRow(r), nil
}

func insertRefresh(ctx context.Context, q *dbgen.Queries, orgID, sessionID uuid.UUID, hash []byte, now time.Time) (RefreshRow, error) {
	r, err := q.InsertRefreshToken(ctx, dbgen.InsertRefreshTokenParams{
		OrgID: orgID, SessionID: sessionID, TokenHash: hash, Now: db.Timestamptz(now),
	})
	if err != nil {
		if name, ok := uniqueViolation(err); ok && (name == constraintRefreshHash || name == constraintRefreshOneLive) {
			return RefreshRow{}, ErrRefreshExists
		}
		return RefreshRow{}, fmt.Errorf("auth: insert refresh token: %w", err)
	}
	return refreshRow(r), nil
}

func (s *pgStore) InsertRefresh(ctx context.Context, orgID, sessionID uuid.UUID, hash []byte, now time.Time) (RefreshRow, error) {
	return insertRefresh(ctx, s.q(ctx), orgID, sessionID, hash, now)
}

func (s *pgStore) FindRefreshByHash(ctx context.Context, hash []byte) (RefreshRow, error) {
	r, err := s.q(ctx).GetRefreshTokenByHash(ctx, hash)
	return oneRefresh(r, err)
}

func (s *pgStore) LockRefresh(ctx context.Context, id uuid.UUID) (RefreshRow, error) {
	q, err := s.qTx(ctx)
	if err != nil {
		return RefreshRow{}, err
	}
	r, err := q.LockRefreshTokenByID(ctx, id)
	return oneRefresh(r, err)
}

// RotateRefresh runs three statements in the caller's transaction: mark the old token used (only
// an unused one matches), insert the successor, link replaced_by. A failure anywhere leaves the
// caller to roll back, so the old token is never half rotated.
func (s *pgStore) RotateRefresh(ctx context.Context, oldID uuid.UUID, newHash []byte, now time.Time) (RefreshRow, error) {
	q, err := s.qTx(ctx)
	if err != nil {
		return RefreshRow{}, err
	}
	old, err := q.MarkRefreshTokenUsed(ctx, dbgen.MarkRefreshTokenUsedParams{ID: oldID, Now: db.Timestamptz(now)})
	if notFound(err) {
		if _, getErr := q.GetRefreshTokenByID(ctx, oldID); getErr == nil {
			return RefreshRow{}, ErrRefreshUsed
		} else if notFound(getErr) {
			return RefreshRow{}, ErrRefreshNotFound
		} else {
			return RefreshRow{}, fmt.Errorf("auth: rotate refresh token: %w", getErr)
		}
	}
	if err != nil {
		return RefreshRow{}, fmt.Errorf("auth: rotate refresh token: %w", err)
	}
	next, err := insertRefresh(ctx, q, old.OrgID, old.SessionID, newHash, now)
	if err != nil {
		return RefreshRow{}, err
	}
	if _, err := q.SetRefreshTokenReplacedBy(ctx, dbgen.SetRefreshTokenReplacedByParams{
		ID: oldID, ReplacedBy: uuid.NullUUID{UUID: next.ID, Valid: true},
	}); err != nil {
		return RefreshRow{}, fmt.Errorf("auth: link refresh token: %w", err)
	}
	return next, nil
}

// ---- admin MFA ----

func mfaRow(m dbgen.AdminMfa) MFARow {
	return MFARow{
		UserID: m.UserID, OrgID: m.OrgID, SecretEnc: m.SecretEnc, PendingSecretEnc: m.PendingSecretEnc,
		PendingCreatedAt: db.TimePtr(m.PendingCreatedAt), EnrolledAt: db.TimePtr(m.EnrolledAt),
		LastUsedStep: m.LastUsedStep, UpdatedAt: db.Time(m.UpdatedAt),
	}
}

func oneMFA(m dbgen.AdminMfa, err error) (MFARow, error) {
	if notFound(err) {
		return MFARow{}, ErrMFANotFound
	}
	if err != nil {
		return MFARow{}, fmt.Errorf("auth: read authenticator: %w", err)
	}
	return mfaRow(m), nil
}

func (s *pgStore) GetMFA(ctx context.Context, userID uuid.UUID) (MFARow, error) {
	m, err := s.q(ctx).GetAdminMFA(ctx, userID)
	return oneMFA(m, err)
}

func (s *pgStore) LockMFA(ctx context.Context, userID uuid.UUID) (MFARow, error) {
	q, err := s.qTx(ctx)
	if err != nil {
		return MFARow{}, err
	}
	m, err := q.LockAdminMFA(ctx, userID)
	return oneMFA(m, err)
}

func (s *pgStore) SetPendingSecret(ctx context.Context, orgID, userID uuid.UUID, enc []byte, now time.Time) error {
	err := s.q(ctx).UpsertPendingMFASecret(ctx, dbgen.UpsertPendingMFASecretParams{
		UserID: userID, OrgID: orgID, PendingSecretEnc: enc, Now: db.Timestamptz(now),
	})
	if err != nil {
		return fmt.Errorf("auth: set pending secret: %w", err)
	}
	return nil
}

func (s *pgStore) ConfirmPending(ctx context.Context, userID uuid.UUID, step int64, now time.Time) error {
	rows, err := s.q(ctx).ConfirmPendingMFASecret(ctx, dbgen.ConfirmPendingMFASecretParams{
		UserID: userID, Step: step, Now: db.Timestamptz(now),
	})
	if err != nil {
		return fmt.Errorf("auth: confirm pending secret: %w", err)
	}
	if rows == 0 {
		return ErrNoPendingSecret
	}
	return nil
}

func (s *pgStore) AdvanceStep(ctx context.Context, userID uuid.UUID, step int64, now time.Time) (bool, error) {
	rows, err := s.q(ctx).AdvanceMFAStep(ctx, dbgen.AdvanceMFAStepParams{UserID: userID, Step: step, Now: db.Timestamptz(now)})
	if err != nil {
		return false, fmt.Errorf("auth: advance step: %w", err)
	}
	return rows > 0, nil
}

// ---- recovery codes ----

func (s *pgStore) ReplaceRecoveryCodes(ctx context.Context, orgID, userID, batchID uuid.UUID, hashes [][]byte, now time.Time) (int, error) {
	q, err := s.qTx(ctx)
	if err != nil {
		return 0, err
	}
	usable, err := q.RevokeRecoveryCodes(ctx, dbgen.RevokeRecoveryCodesParams{UserID: userID, Now: db.Timestamptz(now)})
	if err != nil {
		return 0, fmt.Errorf("auth: revoke recovery codes: %w", err)
	}
	if _, err := q.InsertRecoveryCodes(ctx, dbgen.InsertRecoveryCodesParams{
		OrgID: orgID, UserID: userID, BatchID: batchID, Now: db.Timestamptz(now), CodeHashes: hashes,
	}); err != nil {
		return 0, fmt.Errorf("auth: insert recovery codes: %w", err)
	}
	return int(usable), nil
}

func (s *pgStore) UseRecoveryCode(ctx context.Context, userID uuid.UUID, hash []byte, now time.Time) (remaining int, ok bool, err error) {
	q := s.q(ctx)
	rows, err := q.UseRecoveryCode(ctx, dbgen.UseRecoveryCodeParams{UserID: userID, CodeHash: hash, Now: db.Timestamptz(now)})
	if err != nil {
		return 0, false, fmt.Errorf("auth: use recovery code: %w", err)
	}
	if rows == 0 {
		return 0, false, nil
	}
	left, err := q.CountUsableRecoveryCodes(ctx, userID)
	if err != nil {
		return 0, false, fmt.Errorf("auth: count recovery codes: %w", err)
	}
	return int(left), true, nil
}

func (s *pgStore) CountUsableRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := s.q(ctx).CountUsableRecoveryCodes(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("auth: count recovery codes: %w", err)
	}
	return int(n), nil
}

// ---- challenges ----

func challengeRow(c dbgen.MfaChallenge) ChallengeRow {
	return ChallengeRow{
		ID: c.ID, OrgID: c.OrgID, UserID: c.UserID, DeviceID: c.DeviceID, TokenHash: c.TokenHash,
		Purpose: c.Purpose, DeviceName: c.DeviceName, Platform: c.Platform, ClientType: c.ClientType,
		CreatedAt: db.Time(c.CreatedAt), ExpiresAt: db.Time(c.ExpiresAt), ConsumedAt: db.TimePtr(c.ConsumedAt),
	}
}

func (s *pgStore) CreateChallenge(ctx context.Context, in NewChallenge) (ChallengeRow, error) {
	c, err := s.q(ctx).InsertChallenge(ctx, dbgen.InsertChallengeParams{
		OrgID: in.OrgID, UserID: in.UserID, TokenHash: in.TokenHash, Purpose: in.Purpose, DeviceID: in.DeviceID,
		DeviceName: in.DeviceName, Platform: in.Platform, ClientType: in.ClientType,
		Now: db.Timestamptz(in.Now), ExpiresAt: db.Timestamptz(in.ExpiresAt),
	})
	if err != nil {
		return ChallengeRow{}, fmt.Errorf("auth: create challenge: %w", err)
	}
	return challengeRow(c), nil
}

func (s *pgStore) FindChallengeByHash(ctx context.Context, hash []byte) (ChallengeRow, error) {
	c, err := s.q(ctx).GetChallengeByHash(ctx, hash)
	if notFound(err) {
		return ChallengeRow{}, ErrChallengeNotFound
	}
	if err != nil {
		return ChallengeRow{}, fmt.Errorf("auth: read challenge: %w", err)
	}
	return challengeRow(c), nil
}

func (s *pgStore) ConsumeChallenge(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	rows, err := s.q(ctx).ConsumeChallenge(ctx, dbgen.ConsumeChallengeParams{ID: id, Now: db.Timestamptz(now)})
	if err != nil {
		return false, fmt.Errorf("auth: consume challenge: %w", err)
	}
	return rows > 0, nil
}

// ---- throttles ----

func throttleState(failures []pgtype.Timestamptz, lockedUntil pgtype.Timestamptz) ThrottleState {
	st := ThrottleState{LockedUntil: db.TimePtr(lockedUntil)}
	for _, f := range failures {
		st.Failures = append(st.Failures, db.Time(f))
	}
	return st
}

func (s *pgStore) GetThrottle(ctx context.Context, orgID uuid.UUID, scope string, subject []byte) (ThrottleState, error) {
	r, err := s.q(ctx).GetThrottle(ctx, dbgen.GetThrottleParams{OrgID: orgID, Scope: scope, Subject: subject})
	if notFound(err) {
		return ThrottleState{}, nil
	}
	if err != nil {
		return ThrottleState{}, fmt.Errorf("auth: read throttle: %w", err)
	}
	return throttleState(r.Failures, r.LockedUntil), nil
}

func (s *pgStore) LockThrottle(ctx context.Context, orgID uuid.UUID, scope string, subject []byte, now time.Time) (ThrottleState, error) {
	q, err := s.qTx(ctx)
	if err != nil {
		return ThrottleState{}, err
	}
	if err := q.EnsureThrottle(ctx, dbgen.EnsureThrottleParams{OrgID: orgID, Scope: scope, Subject: subject, Now: db.Timestamptz(now)}); err != nil {
		return ThrottleState{}, fmt.Errorf("auth: ensure throttle: %w", err)
	}
	r, err := q.LockThrottle(ctx, dbgen.LockThrottleParams{OrgID: orgID, Scope: scope, Subject: subject})
	if err != nil {
		return ThrottleState{}, fmt.Errorf("auth: lock throttle: %w", err)
	}
	return throttleState(r.Failures, r.LockedUntil), nil
}

func (s *pgStore) SaveThrottle(ctx context.Context, orgID uuid.UUID, scope string, subject []byte, st ThrottleState, now time.Time) error {
	q, err := s.qTx(ctx)
	if err != nil {
		return err
	}
	failures := make([]pgtype.Timestamptz, 0, len(st.Failures)) // never nil: the column is NOT NULL
	for _, f := range st.Failures {
		failures = append(failures, db.Timestamptz(f))
	}
	err = q.UpsertThrottle(ctx, dbgen.UpsertThrottleParams{
		OrgID: orgID, Scope: scope, Subject: subject, Failures: failures,
		LockedUntil: db.NullTimestamptz(st.LockedUntil), Now: db.Timestamptz(now),
	})
	if err != nil {
		return fmt.Errorf("auth: save throttle: %w", err)
	}
	return nil
}

func (s *pgStore) DeleteThrottle(ctx context.Context, orgID uuid.UUID, scope string, subject []byte) error {
	q, err := s.qTx(ctx)
	if err != nil {
		return err
	}
	if err := q.DeleteThrottle(ctx, dbgen.DeleteThrottleParams{OrgID: orgID, Scope: scope, Subject: subject}); err != nil {
		return fmt.Errorf("auth: delete throttle: %w", err)
	}
	return nil
}
