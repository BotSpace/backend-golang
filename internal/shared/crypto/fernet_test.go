package crypto

import (
	"encoding/hex"
	"testing"
)

// TestFernetDecrypt_KnownVector cross-checks FernetDecrypt against a token
// generated with Python's cryptography.fernet.Fernet 46.0.3, using exactly
// Django's key derivation (core/apps/credentials/services/crypto.py):
//
//	SECRET_KEY = "test-django-secret-key-for-fernet-vector"
//	key        = base64.urlsafe_b64encode(sha256(SECRET_KEY.encode()).digest())
//	payload    = {"api_key": "sk-test-1234567890", "base_url": "https://api.openai.com/v1"}
//	token      = Fernet(key).encrypt(json.dumps(payload).encode())
func TestFernetDecrypt_KnownVector(t *testing.T) {
	const secretKey = "test-django-secret-key-for-fernet-vector"
	const wantDigestHex = "5caa47f61138f450ad4ed4bb765ca41fb9fdda793946e0f8ee1443675d838374"
	const token = "gAAAAABqR-C5OMteBhgt9bCC3W2KwriG1N9U4LuU9wsqK5ABnwL2LP_jhE-c6gmj80vvTHv0NmmAk6i4NSiHPvumZ1jtkafx_XVBvGh7ZaS_98n1PADiO9swS77GgWGm1vdMNrbCYyWQdGOADrp_Ry0zZwhf86nILw5daZjPupEG9UiXxlzviqc="
	const wantPlain = `{"api_key": "sk-test-1234567890", "base_url": "https://api.openai.com/v1"}`

	key := DjangoFernetKey(secretKey)
	if got := hex.EncodeToString(key[:]); got != wantDigestHex {
		t.Fatalf("DjangoFernetKey digest = %s, want %s", got, wantDigestHex)
	}

	plain, err := FernetDecrypt(key, token)
	if err != nil {
		t.Fatalf("FernetDecrypt: %v", err)
	}
	if string(plain) != wantPlain {
		t.Fatalf("FernetDecrypt = %q, want %q", plain, wantPlain)
	}
}

func TestFernetDecrypt_RejectsTamperedToken(t *testing.T) {
	key := DjangoFernetKey("some-other-secret")
	// Valid base64/length/version but garbage HMAC+ciphertext.
	tampered := "gAAAAABqR-C5OMteBhgt9bCC3W2KwriG1N9U4LuU9wsqK5ABnwL2LP_jhE-c6gmj80vvTHv0NmmAk6i4NSiHPvumZ1jtkafx_XVBvGh7ZaS_98n1PADiO9swS77GgWGm1vdMNrbCYyWQdGOADrp_Ry0zZwhf86nILw5daZjPupEG9UiXxlzviqc="
	if _, err := FernetDecrypt(key, tampered); err == nil {
		t.Fatal("FernetDecrypt accepted a token signed with the wrong key")
	}
}

func TestFernetDecrypt_RejectsShortToken(t *testing.T) {
	key := DjangoFernetKey("whatever")
	if _, err := FernetDecrypt(key, "AAAA"); err == nil {
		t.Fatal("FernetDecrypt accepted an implausibly short token")
	}
}

// TestFernetDecrypt_RejectsAESGCMOutput is the "is this already migrated"
// signal cmd/reencrypt-credentials relies on: base64.StdEncoding output from
// crypto.Cipher.Encrypt (nonce[12]||ciphertext||tag[16], no PKCS7 padding,
// no HMAC trailer) must not be mistaken for a valid Fernet token.
func TestFernetDecrypt_RejectsAESGCMOutput(t *testing.T) {
	fernetKey := DjangoFernetKey("secret-a")
	c, err := New([]byte("app-crypto-key-secret-b"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	aesToken, err := c.Encrypt([]byte(`{"api_key":"already-migrated"}`))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := FernetDecrypt(fernetKey, aesToken); err == nil {
		t.Fatal("FernetDecrypt accepted AES-GCM ciphertext as a valid Fernet token")
	}
}
