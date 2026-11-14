package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

// Compile skeleton (S08): every method returns errNotImplemented. S10 implements the adapter; this
// is the only file of the package that may import pgx.
var errNotImplemented = errors.New("auth: store not implemented")

type pgStore struct{ txr db.TxRunner }

// NewPGStore returns the PostgreSQL-backed Store.
func NewPGStore(txr db.TxRunner) Store { return &pgStore{txr: txr} }

var _ Store = (*pgStore)(nil)

func (s *pgStore) DefaultOrg(_ context.Context) (uuid.UUID, error) {
	return uuid.UUID{}, errNotImplemented
}

func (s *pgStore) CreateUser(_ context.Context, in NewUser) (UserRow, error) {
	return UserRow{}, errNotImplemented
}

func (s *pgStore) GetUser(_ context.Context, id uuid.UUID) (UserRow, error) {
	return UserRow{}, errNotImplemented
}

func (s *pgStore) FindUserByIdentifier(_ context.Context, kind IdentifierKind, value string) (UserRow, error) {
	return UserRow{}, errNotImplemented
}

func (s *pgStore) LockUser(_ context.Context, id uuid.UUID) (UserRow, error) {
	return UserRow{}, errNotImplemented
}

func (s *pgStore) LockAdmins(_ context.Context, orgID uuid.UUID) ([]UserRow, error) {
	return nil, errNotImplemented
}

func (s *pgStore) SetStatus(_ context.Context, id uuid.UUID, to, before Status, now time.Time) error {
	return errNotImplemented
}

func (s *pgStore) MarkVerified(_ context.Context, id uuid.UUID, kind IdentifierKind, now time.Time) (bool, error) {
	return false, errNotImplemented
}

func (s *pgStore) ReplaceContact(_ context.Context, id uuid.UUID, kind IdentifierKind, value string, now time.Time) error {
	return errNotImplemented
}

func (s *pgStore) ChangePassword(_ context.Context, id uuid.UUID, phc string, now time.Time) error {
	return errNotImplemented
}

func (s *pgStore) Rehash(_ context.Context, id uuid.UUID, oldPHC, newPHC string, now time.Time) (bool, error) {
	return false, errNotImplemented
}

func (s *pgStore) RecordSignIn(_ context.Context, id uuid.UUID, now time.Time) error {
	return errNotImplemented
}

func (s *pgStore) CreateSession(_ context.Context, in NewSession) (SessionRow, error) {
	return SessionRow{}, errNotImplemented
}

func (s *pgStore) GetSession(_ context.Context, id uuid.UUID) (SessionRow, error) {
	return SessionRow{}, errNotImplemented
}

func (s *pgStore) LockSession(_ context.Context, id uuid.UUID) (SessionRow, error) {
	return SessionRow{}, errNotImplemented
}

func (s *pgStore) GetSessionPrincipal(_ context.Context, sessionID uuid.UUID) (SessionPrincipal, error) {
	return SessionPrincipal{}, errNotImplemented
}

func (s *pgStore) ListLiveSessions(_ context.Context, userID uuid.UUID, now time.Time) ([]SessionRow, error) {
	return nil, errNotImplemented
}

func (s *pgStore) TouchSession(_ context.Context, id uuid.UUID, now, idleExpiresAt time.Time) error {
	return errNotImplemented
}

func (s *pgStore) EndSession(_ context.Context, id uuid.UUID, reason string, now time.Time) (bool, error) {
	return false, errNotImplemented
}

func (s *pgStore) EndUserSessions(_ context.Context, userID uuid.UUID, exceptSessionID uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error) {
	return nil, errNotImplemented
}

func (s *pgStore) InsertRefresh(_ context.Context, orgID, sessionID uuid.UUID, hash []byte, now time.Time) (RefreshRow, error) {
	return RefreshRow{}, errNotImplemented
}

func (s *pgStore) FindRefreshByHash(_ context.Context, hash []byte) (RefreshRow, error) {
	return RefreshRow{}, errNotImplemented
}

func (s *pgStore) LockRefresh(_ context.Context, id uuid.UUID) (RefreshRow, error) {
	return RefreshRow{}, errNotImplemented
}

func (s *pgStore) RotateRefresh(_ context.Context, oldID uuid.UUID, newHash []byte, now time.Time) (RefreshRow, error) {
	return RefreshRow{}, errNotImplemented
}

func (s *pgStore) GetMFA(_ context.Context, userID uuid.UUID) (MFARow, error) {
	return MFARow{}, errNotImplemented
}

func (s *pgStore) LockMFA(_ context.Context, userID uuid.UUID) (MFARow, error) {
	return MFARow{}, errNotImplemented
}

func (s *pgStore) SetPendingSecret(_ context.Context, orgID, userID uuid.UUID, enc []byte, now time.Time) error {
	return errNotImplemented
}

func (s *pgStore) ConfirmPending(_ context.Context, userID uuid.UUID, step int64, now time.Time) error {
	return errNotImplemented
}

func (s *pgStore) AdvanceStep(_ context.Context, userID uuid.UUID, step int64, now time.Time) (bool, error) {
	return false, errNotImplemented
}

func (s *pgStore) ReplaceRecoveryCodes(_ context.Context, orgID, userID, batchID uuid.UUID, hashes [][]byte, now time.Time) (int, error) {
	return 0, errNotImplemented
}

func (s *pgStore) UseRecoveryCode(_ context.Context, userID uuid.UUID, hash []byte, now time.Time) (remaining int, ok bool, err error) {
	return 0, false, errNotImplemented
}

func (s *pgStore) CountUsableRecoveryCodes(_ context.Context, userID uuid.UUID) (int, error) {
	return 0, errNotImplemented
}

func (s *pgStore) CreateChallenge(_ context.Context, in NewChallenge) (ChallengeRow, error) {
	return ChallengeRow{}, errNotImplemented
}

func (s *pgStore) FindChallengeByHash(_ context.Context, hash []byte) (ChallengeRow, error) {
	return ChallengeRow{}, errNotImplemented
}

func (s *pgStore) ConsumeChallenge(_ context.Context, id uuid.UUID, now time.Time) (bool, error) {
	return false, errNotImplemented
}

func (s *pgStore) GetThrottle(_ context.Context, orgID uuid.UUID, scope string, subject []byte) (ThrottleState, error) {
	return ThrottleState{}, errNotImplemented
}

func (s *pgStore) LockThrottle(_ context.Context, orgID uuid.UUID, scope string, subject []byte, now time.Time) (ThrottleState, error) {
	return ThrottleState{}, errNotImplemented
}

func (s *pgStore) SaveThrottle(_ context.Context, orgID uuid.UUID, scope string, subject []byte, st ThrottleState, now time.Time) error {
	return errNotImplemented
}

func (s *pgStore) DeleteThrottle(_ context.Context, orgID uuid.UUID, scope string, subject []byte) error {
	return errNotImplemented
}
