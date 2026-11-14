package auth

import (
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"
)

const (
	totpPeriod     = 30
	totpIssuer     = "Swastik"
	totpSecretSize = 20
	totpMinSecret  = 10 // decoded bytes; shorter secrets are never produced by GenerateTOTP
)

var totpBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTP creates a 20-byte secret (upper-case base32 without padding) and its otpauth URI
// (SHA-1, 6 digits, 30 seconds: what authenticator apps expect).
func GenerateTOTP(accountName string) (secret, uri string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: accountName,
		Period:      totpPeriod,
		SecretSize:  totpSecretSize,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", fmt.Errorf("auth: totp secret: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// TOTPStepAt is floor(unix seconds / 30).
func TOTPStepAt(t time.Time) int64 { return t.Unix() / totpPeriod }

// VerifyTOTP checks code against steps N-1, N and N+1 around now. A candidate counts only if its
// step is greater than lastUsedStep (replay guard); the matched step is returned for the caller to
// store. A malformed secret or a code that is not exactly six ASCII digits yields (0, false). All
// candidates are evaluated before the decision.
func VerifyTOTP(secret, code string, now time.Time, lastUsedStep int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return 0, false
		}
	}
	raw, err := totpBase32.DecodeString(secret)
	if err != nil || len(raw) < totpMinSecret {
		return 0, false
	}
	n := TOTPStepAt(now)
	var matched int64
	found := false
	for _, step := range []int64{n - 1, n, n + 1} {
		want, err := hotp.GenerateCodeCustom(secret, uint64(step), hotp.ValidateOpts{ //nolint:gosec // step is positive for any realistic clock
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 && step > lastUsedStep && !found {
			matched, found = step, true
		}
	}
	return matched, found
}
