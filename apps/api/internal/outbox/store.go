package outbox

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
)

const (
	maxTopicLen      = 64
	maxKeyLen        = 200
	maxAttemptsLimit = 50
)

var topicRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// validate checks a message against the table constraints so that a bad message is rejected
// before any SQL runs (a failed statement would abort the caller's transaction). It returns the
// encoded payload.
func validate(m Message) ([]byte, error) {
	switch {
	case m.OrgID == uuid.Nil:
		return nil, invalid("org is required")
	case len(m.Topic) > maxTopicLen || !topicRE.MatchString(m.Topic):
		return nil, invalid("topic %q must look like <audience>.<event> (lower case, at most %d characters)", m.Topic, maxTopicLen)
	case m.Subject == "" || len(m.Subject) > maxKeyLen:
		return nil, invalid("subject must be 1 to %d characters", maxKeyLen)
	case m.DedupeKey == "" || len(m.DedupeKey) > maxKeyLen:
		return nil, invalid("dedupe key must be 1 to %d characters", maxKeyLen)
	case m.MaxAttempts < 0 || m.MaxAttempts > maxAttemptsLimit:
		return nil, invalid("max attempts must be between 0 and %d", maxAttemptsLimit)
	}
	switch m.Recipient.Kind {
	case "client":
		if m.Recipient.ID == nil || *m.Recipient.ID == uuid.Nil {
			return nil, invalid("a client recipient needs an id")
		}
	case "admin":
	default:
		return nil, invalid("recipient kind %q must be client or admin", m.Recipient.Kind)
	}
	payload, err := db.MarshalPayload(m.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return payload, nil
}

// maxAttemptsFor resolves the attempt limit: the message, else the topic's sender, else the default.
func (s *Service) maxAttemptsFor(m Message) int {
	if m.MaxAttempts > 0 {
		return m.MaxAttempts
	}
	if r, ok := s.registrationFor(m.Topic); ok && r.maxAttempts > 0 {
		return r.maxAttempts
	}
	return s.opts.DefaultMaxAttempts
}

func envelopeOf(m dbgen.OutboxMessage) Envelope {
	e := Envelope{
		ID: m.ID, OrgID: m.OrgID, Topic: m.Topic, Subject: m.Subject, DedupeKey: m.DedupeKey,
		Payload: m.Payload, Attempt: int(m.Attempts), CreatedAt: db.Time(m.CreatedAt),
		Recipient: Recipient{Kind: m.RecipientKind},
	}
	if m.RecipientID.Valid {
		id := m.RecipientID.UUID
		e.Recipient.ID = &id
	}
	return e
}

// cursor is the keyset position (last_attempt_at, id) of the last item of a page.
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
