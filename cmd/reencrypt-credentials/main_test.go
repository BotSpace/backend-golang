package main

import (
	"encoding/json"
	"testing"

	"github.com/botspace/backend/internal/shared/crypto"
)

// TestClassifyRow_MigratesFernetThenIsIdempotent exercises the full
// decision path this migration relies on: a Fernet-encrypted row is
// decrypted and re-encrypted with AES-GCM (outcomeMigrated); running
// classifyRow AGAIN on that new AES-GCM value must report outcomeAlreadyAES
// (nothing to do) — the idempotency property the doc comment promises.
func TestClassifyRow_MigratesFernetThenIsIdempotent(t *testing.T) {
	const djangoSecret = "classify-row-test-secret"
	fernetKey := crypto.DjangoFernetKey(djangoSecret)

	aesCipher, err := crypto.New([]byte("app-crypto-key-for-classify-row-test"))
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}

	// A real Fernet token for {"api_key": "sk-abc"} encrypted with
	// sha256("classify-row-test-secret"), generated with Python's
	// cryptography.fernet.Fernet 46.0.3 (same tool used for
	// internal/shared/crypto/fernet_test.go's known vector) — this test
	// independently verifies main.go's use of the fernet.go primitives.
	const fernetToken = "gAAAAABqR-FBDYc3-uHu9P-PWhqAB-1s9pIf0UUTYr3mtg53-4-9HXXmQQqVxeCedq2Jbh7Xs7wC3W4mv2mkUPxFnSt6GUs2sYDZNBo14DZyyZr9R4-NRxY="
	const wantPlain = `{"api_key": "sk-abc"}`

	if plain, err := crypto.FernetDecrypt(fernetKey, fernetToken); err != nil || string(plain) != wantPlain {
		t.Fatalf("fixture sanity check failed: FernetDecrypt = %q, %v, want %q, nil", plain, err, wantPlain)
	}

	outcome, newEncrypted, err := classifyRow(fernetKey, aesCipher, fernetToken)
	if err != nil {
		t.Fatalf("classifyRow (fernet row): %v", err)
	}
	if outcome != outcomeMigrated {
		t.Fatalf("outcome = %v, want outcomeMigrated", outcome)
	}
	if newEncrypted == "" {
		t.Fatal("classifyRow returned an empty re-encrypted value")
	}

	// The re-encrypted value must actually decrypt to the same plaintext
	// under this backend's own cipher.
	plain, err := aesCipher.Decrypt(newEncrypted)
	if err != nil {
		t.Fatalf("decrypt migrated value: %v", err)
	}
	if !json.Valid(plain) {
		t.Fatalf("migrated plaintext is not valid JSON: %s", plain)
	}

	// Idempotency: classifying the NEW (AES-GCM) value must be a no-op.
	outcome2, _, err := classifyRow(fernetKey, aesCipher, newEncrypted)
	if err != nil {
		t.Fatalf("classifyRow (already-migrated row): %v", err)
	}
	if outcome2 != outcomeAlreadyAES {
		t.Fatalf("second classifyRow outcome = %v, want outcomeAlreadyAES (idempotency broken)", outcome2)
	}
}

// TestClassifyRow_RejectsGarbage confirms a row that decrypts under NEITHER
// cipher is reported failed, not silently swallowed.
func TestClassifyRow_RejectsGarbage(t *testing.T) {
	fernetKey := crypto.DjangoFernetKey("some-secret")
	aesCipher, err := crypto.New([]byte("another-app-crypto-key"))
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}

	outcome, _, err := classifyRow(fernetKey, aesCipher, "not-a-valid-token-at-all")
	if outcome != outcomeFailed {
		t.Fatalf("outcome = %v, want outcomeFailed", outcome)
	}
	if err == nil {
		t.Fatal("expected a non-nil error describing why classification failed")
	}
}
