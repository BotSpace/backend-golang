package credential

import (
	"testing"
	"time"
)

// TestDjangoSignature_KnownVector cross-checks djangoSignature against a
// vector generated with Django 5.2.9's django.core.signing.TimestampSigner
// (see oauth_state.go's package comment for how it was produced):
//
//	SECRET_KEY = "test-secret-key-for-vector"
//	payload    = "42:google_email:abcdef0123456789"
//	timestamp  = 1700000000 (forced, base62 "1r31eq")
//	signed     = "42:google_email:abcdef0123456789:1r31eq:3kA8FpRy_YptBxDzANKDliUJkpm2FZrwFN48Bn9npds"
func TestDjangoSignature_KnownVector(t *testing.T) {
	const secretKey = "test-secret-key-for-vector"
	const payload = "42:google_email:abcdef0123456789"
	const wantTS = "1r31eq"
	const wantSig = "3kA8FpRy_YptBxDzANKDliUJkpm2FZrwFN48Bn9npds"

	if got := base62Encode(1700000000); got != wantTS {
		t.Fatalf("base62Encode(1700000000) = %q, want %q", got, wantTS)
	}
	if got, err := base62Decode(wantTS); err != nil || got != 1700000000 {
		t.Fatalf("base62Decode(%q) = %d, %v, want 1700000000, nil", wantTS, got, err)
	}

	timestamped := payload + stateSep + wantTS
	if got := djangoSignature(secretKey, timestamped); got != wantSig {
		t.Fatalf("djangoSignature = %q, want %q", got, wantSig)
	}

	full := timestamped + stateSep + wantSig
	uid, typeKey, err := verifyState(secretKey, full, 0) // maxAge=0: skip expiry check
	if err != nil {
		t.Fatalf("verifyState: %v", err)
	}
	if uid != "42" || typeKey != "google_email" {
		t.Fatalf("verifyState = (%q, %q), want (42, google_email)", uid, typeKey)
	}
}

// TestSignState_RoundTrip exercises the real entry point (signState, called
// by the OAuth start handler): sign now, verify immediately, expect no error
// and the same user/type back.
func TestSignState_RoundTrip(t *testing.T) {
	const secretKey = "another-secret"
	nonce, err := newNonce()
	if err != nil {
		t.Fatalf("newNonce: %v", err)
	}
	state := signState(secretKey, "7", "google_sheets", nonce)

	uid, typeKey, err := verifyState(secretKey, state, stateMaxAgeSeconds*time.Second)
	if err != nil {
		t.Fatalf("verifyState: %v", err)
	}
	if uid != "7" || typeKey != "google_sheets" {
		t.Fatalf("verifyState = (%q, %q), want (7, google_sheets)", uid, typeKey)
	}
}

// TestVerifyState_RejectsTamperedSignature ensures a flipped character in
// the signature is rejected — the whole point of porting this algorithm.
func TestVerifyState_RejectsTamperedSignature(t *testing.T) {
	const secretKey = "yet-another-secret"
	nonce, _ := newNonce()
	state := signState(secretKey, "1", "google_email", nonce)
	tampered := state[:len(state)-1] + "x"

	if _, _, err := verifyState(secretKey, tampered, 0); err == nil {
		t.Fatal("verifyState accepted a tampered signature")
	}
}

// TestVerifyState_RejectsExpired confirms the max-age check fires for an
// old timestamp.
func TestVerifyState_RejectsExpired(t *testing.T) {
	const secretKey = "expiry-secret"
	payload := "1:google_email:deadbeefdeadbeef"
	oldTS := base62Encode(time.Now().Add(-1 * time.Hour).Unix())
	timestamped := payload + stateSep + oldTS
	state := timestamped + stateSep + djangoSignature(secretKey, timestamped)

	if _, _, err := verifyState(secretKey, state, stateMaxAgeSeconds*time.Second); err == nil {
		t.Fatal("verifyState accepted an expired state token")
	}
}
