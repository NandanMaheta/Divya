package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// MaxPayloadBytes is the largest payload (in the text form PostgreSQL gives a jsonb value) a
// scheduled job or outbox message may carry; the tables enforce the same limit with a CHECK.
const MaxPayloadBytes = 8192

// ErrPayload is returned by MarshalPayload for a value that cannot be encoded or is too large.
var ErrPayload = errors.New("db: invalid payload")

// Timestamptz converts an instant to a non-null pgx timestamp.
func Timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// NullTimestamptz converts an optional instant; nil is SQL NULL.
func NullTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return Timestamptz(*t)
}

// TimePtr converts a nullable pgx timestamp to a UTC instant, nil for NULL.
func TimePtr(p pgtype.Timestamptz) *time.Time {
	if !p.Valid {
		return nil
	}
	t := p.Time.UTC()
	return &t
}

// Time converts a pgx timestamp to a UTC instant (the zero time for NULL).
func Time(p pgtype.Timestamptz) time.Time {
	if !p.Valid {
		return time.Time{}
	}
	return p.Time.UTC()
}

// TextPtr converts a nullable pgx text to a string pointer, nil for NULL.
func TextPtr(p pgtype.Text) *string {
	if !p.Valid {
		return nil
	}
	return &p.String
}

// MarshalPayload JSON-encodes v for a jsonb payload column. nil becomes {}. It returns ErrPayload
// when v cannot be encoded or would not fit MaxPayloadBytes once PostgreSQL has rendered it, so
// callers can reject the value before sending any SQL (which would poison their transaction).
func MarshalPayload(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPayload, err)
	}
	if string(b) == "null" {
		return []byte("{}"), nil
	}
	if n := jsonbTextLen(b); n > MaxPayloadBytes {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrPayload, n, MaxPayloadBytes)
	}
	return b, nil
}

// jsonbTextLen is the length of compact JSON once PostgreSQL renders it as jsonb text, which adds
// one space after every ':' and ',' outside strings.
func jsonbTextLen(compact []byte) int {
	n := len(compact)
	inString, escaped := false, false
	for _, c := range compact {
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && (c == ':' || c == ','):
			n++
		}
	}
	return n
}

// Text converts a string to a non-null pgx text.
func Text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
