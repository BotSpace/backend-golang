// Fernet decryption — read-only, stdlib-only port of the subset of
// cryptography.fernet.Fernet this backend needs: decrypting
// credential.data_encrypted rows the ETL carried over as-is from Django
// (still Fernet-encrypted with Django's SECRET_KEY — see
// scripts/etl_django.py's SKIPPED_NOTES and cmd/reencrypt-credentials).
//
// Fernet token format (github.com/fernet/spec, matches Django's
// core/apps/credentials/services/crypto.py -> cryptography.fernet.Fernet):
//
//	base64url( version[1]=0x80 || timestamp[8]=int64 BE ||
//	           iv[16] || ciphertext[N, PKCS7-padded, N%16==0] || hmac[32] )
//
// Django's key resolution (core/apps/credentials/services/crypto.py
// _get_fernet): CREDENTIALS_FERNET_KEY if set, else
// base64.urlsafe_b64encode(sha256(settings.SECRET_KEY)). The Fernet
// constructor base64-decodes that back to the raw 32 bytes, so — since this
// backend never sets CREDENTIALS_FERNET_KEY — the raw Fernet key is simply
// sha256(DJANGO_SECRET_KEY), and DjangoFernetKey below produces exactly
// that, skipping the redundant encode/decode round trip.
//
// A Fernet key's 32 raw bytes split into signingKey=key[:16] (HMAC-SHA256)
// and encKey=key[16:32] (AES-128-CBC) — see FernetDecrypt.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

var (
	ErrFernetTooShort     = errors.New("fernet: token too short")
	ErrFernetBadVersion   = errors.New("fernet: unsupported version byte")
	ErrFernetBadSignature = errors.New("fernet: HMAC signature mismatch")
	ErrFernetBadPadding   = errors.New("fernet: invalid PKCS7 padding")
)

const (
	fernetVersion   = 0x80
	fernetHeaderLen = 1 + 8 + 16 // version + timestamp + iv
	fernetHMACLen   = 32
	fernetMinLen    = fernetHeaderLen + 16 + fernetHMACLen // + at least one AES block of ciphertext
)

// DjangoFernetKey derives the raw 32-byte Fernet key Django's
// core.apps.credentials.services.crypto._get_fernet() falls back to:
// sha256(secretKey). Callers that split it via signingKey/encKey below get
// the exact same effective key Fernet(base64.urlsafe_b64encode(digest))
// would use internally.
func DjangoFernetKey(secretKey string) [32]byte {
	return sha256.Sum256([]byte(secretKey))
}

// FernetDecrypt decrypts a Fernet token (a Django credential.data_encrypted
// value) given the raw 32-byte key (see DjangoFernetKey). It does NOT check
// the embedded timestamp against a TTL — these tokens have no expiry in
// Django's usage (a stored secret, not a short-lived session token).
func FernetDecrypt(key32 [32]byte, token string) ([]byte, error) {
	signingKey := key32[:16]
	encKey := key32[16:32]

	raw, err := decodeFernetBase64(token)
	if err != nil {
		return nil, fmt.Errorf("fernet: base64 decode: %w", err)
	}
	if len(raw) < fernetMinLen {
		return nil, ErrFernetTooShort
	}
	if raw[0] != fernetVersion {
		return nil, ErrFernetBadVersion
	}

	signed := raw[:len(raw)-fernetHMACLen]
	gotMAC := raw[len(raw)-fernetHMACLen:]
	mac := hmac.New(sha256.New, signingKey)
	mac.Write(signed)
	if !hmac.Equal(gotMAC, mac.Sum(nil)) {
		return nil, ErrFernetBadSignature
	}

	iv := raw[9:25]
	ciphertext := raw[25 : len(raw)-fernetHMACLen]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("fernet: ciphertext length %d not a multiple of %d", len(ciphertext), aes.BlockSize)
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, fmt.Errorf("fernet: new aes cipher: %w", err)
	}
	plainPadded := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plainPadded, ciphertext)

	return pkcs7Unpad(plainPadded)
}

// decodeFernetBase64 mirrors Python's base64.urlsafe_b64decode, which
// tolerates (ignores) a missing/short padding as long as the un-padded
// length is provided — Go's base64.URLEncoding requires exact padding, so
// try as-is first, then re-pad to the nearest multiple of 4.
func decodeFernetBase64(s string) ([]byte, error) {
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if pad := len(s) % 4; pad != 0 {
		s += "===="[pad:]
	}
	return base64.URLEncoding.DecodeString(s)
}

// pkcs7Unpad removes PKCS7 padding, validating it strictly (every pad byte
// must equal the pad length, and the pad length must be 1..blockSize) — a
// corrupt/mis-decrypted buffer almost always fails this check, which is the
// main signal cmd/reencrypt-credentials uses to tell "this row is Fernet"
// from "this row is already AES-GCM" without a format tag on the column.
func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, ErrFernetBadPadding
	}
	padLen := int(b[len(b)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(b) {
		return nil, ErrFernetBadPadding
	}
	for _, c := range b[len(b)-padLen:] {
		if int(c) != padLen {
			return nil, ErrFernetBadPadding
		}
	}
	return b[:len(b)-padLen], nil
}
