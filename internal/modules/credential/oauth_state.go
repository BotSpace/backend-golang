package credential

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file ports django.core.signing's TimestampSigner byte-for-byte. Both
// OAuthStart (mints the state) and OAuthCallback (verifies it) now live on
// this backend, but the algorithm stays Django-signing-compatible on
// purpose: the signing key is DJANGO_SECRET_KEY (see usecase.go's
// oauthStateSecret), shared with the still-live Django deployment, and a
// state token minted moments before this endpoint's redirect_uri changed
// (or by any other caller still assuming Django's callback) must still
// verify correctly here.
//
// Verified against a known-answer vector generated with Django 5.2.9's
// django.core.signing.TimestampSigner (see oauth_state_test.go) — do not
// change the salt/algorithm/format without re-deriving that vector.

const (
	// djangoTimestampSignerSalt is Signer.__init__'s default salt for
	// TimestampSigner(): "%s.%s" % (cls.__module__, cls.__name__).
	djangoTimestampSignerSalt = "django.core.signing.TimestampSigner"
	stateSep                  = ":"
	stateMaxAgeSeconds        = 600 // apps/credentials/services/oauth.py STATE_MAX_AGE
	base62Alphabet            = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var errBadSignature = errors.New("oauth: bad state signature")

// signState ports make_state(user_id, type_key): payload = "user_id:type_key:nonce",
// signed = payload + ":" + base62(now) + ":" + signature(payload + ":" + base62(now)).
func signState(secretKey, userID, typeKey, nonce string) string {
	payload := strings.Join([]string{userID, typeKey, nonce}, stateSep)
	timestamped := payload + stateSep + base62Encode(time.Now().Unix())
	return timestamped + stateSep + djangoSignature(secretKey, timestamped)
}

// verifyState ports verify_state — called by OAuthCallback (usecase.go) on
// every Google redirect, and directly by tests so signing round-trips and
// drifts get caught by CI rather than discovered live against Google.
func verifyState(secretKey, state string, maxAge time.Duration) (userID, typeKey string, err error) {
	lastSep := strings.LastIndex(state, stateSep)
	if lastSep < 0 {
		return "", "", errBadSignature
	}
	value, sig := state[:lastSep], state[lastSep+1:]
	if !hmac.Equal([]byte(sig), []byte(djangoSignature(secretKey, value))) {
		return "", "", errBadSignature
	}

	tsSep := strings.LastIndex(value, stateSep)
	if tsSep < 0 {
		return "", "", errBadSignature
	}
	payload, tsRaw := value[:tsSep], value[tsSep+1:]
	ts, err := base62Decode(tsRaw)
	if err != nil {
		return "", "", fmt.Errorf("oauth: bad state timestamp: %w", err)
	}
	if maxAge > 0 && time.Since(time.Unix(ts, 0)) > maxAge {
		return "", "", errors.New("oauth: state expired")
	}

	parts := strings.SplitN(payload, stateSep, 3)
	if len(parts) < 3 {
		return "", "", errors.New("oauth: malformed state payload")
	}
	return parts[0], parts[1], nil
}

// djangoSignature ports Signer.signature(value): base64_hmac(salt+"signer", value, key),
// i.e. base64url_nopad(HMAC-SHA256(SHA256(salt+"signer" + key), value)).
func djangoSignature(secretKey, value string) string {
	keySalt := djangoTimestampSignerSalt + "signer"
	derivedKey := sha256.Sum256([]byte(keySalt + secretKey))
	mac := hmac.New(sha256.New, derivedKey[:])
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// base62Encode ports django.core.signing.b62_encode (non-negative int64
// input only — timestamps are always non-negative here).
func base62Encode(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{base62Alphabet[n%62]}, buf...)
		n /= 62
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}

func base62Decode(s string) (int64, error) {
	if s == "0" {
		return 0, nil
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	var n int64
	for _, c := range s {
		idx := strings.IndexRune(base62Alphabet, c)
		if idx < 0 {
			return 0, fmt.Errorf("invalid base62 digit %q", c)
		}
		n = n*62 + int64(idx)
	}
	if neg {
		n = -n
	}
	return n, nil
}

// newNonce mirrors secrets.token_hex(8) — 16 hex chars from 8 random bytes.
func newNonce() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}
