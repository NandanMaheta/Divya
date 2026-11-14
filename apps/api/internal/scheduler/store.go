package scheduler

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

// validate checks a spec against the table constraints before any SQL runs: a failed statement
// would abort the caller's transaction. It returns the encoded payload.
func validate(spec JobSpec) ([]byte, error) {
	switch {
	case spec.OrgID == uuid.Nil:
		return nil, invalid("org is required")
	case len(spec.Kind) > maxKindLen || !kindRE.MatchString(spec.Kind):
		return nil, invalid("kind %q must look like <module>.<action> (lower case, at most %d characters)", spec.Kind, maxKindLen)
	case spec.Key == "" || len(spec.Key) > maxKeyLen:
		return nil, invalid("key must be 1 to %d characters", maxKeyLen)
	case spec.Subject == "" || len(spec.Subject) > maxKeyLen:
		return nil, invalid("subject must be 1 to %d characters", maxKeyLen)
	case spec.RunAt.IsZero():
		return nil, invalid("run_at is required")
	}
	payload, err := db.MarshalPayload(spec.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return payload, nil
}

// The cursor is the keyset position (last_attempt_at, id) of the last item of a page.
func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, invalid("malformed cursor")
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, invalid("malformed cursor")
	}
	t, terr := time.Parse(time.RFC3339Nano, at)
	u, uerr := uuid.Parse(id)
	if err := errors.Join(terr, uerr); err != nil {
		return time.Time{}, uuid.Nil, invalid("malformed cursor")
	}
	return t, u, nil
}
