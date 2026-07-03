// Command reencrypt-credentials is a one-shot data migration: every
// `credential.data_encrypted` row imported by scripts/etl_django.py is still
// Fernet-encrypted with Django's SECRET_KEY (see that script's SKIPPED_NOTES
// and internal/modules/credential/registry.go) — this backend's own crypto
// (internal/shared/crypto, AES-256-GCM keyed by APP_CRYPTO_KEY) cannot read
// them. This command decrypts each row with Django's Fernet scheme and
// re-encrypts it with this backend's cipher, in place.
//
// Needs TWO secrets, both already present in the cluster (see the k8s
// secrets `django-secret.DJANGO_SECRET_KEY` and
// `backend-golang-app.APP_CRYPTO_KEY`):
//
//   - DJANGO_SECRET_KEY — Django's settings.SECRET_KEY. The Fernet key
//     credential data was encrypted with is sha256(DJANGO_SECRET_KEY) — see
//     internal/shared/crypto/fernet.go's package doc for the derivation.
//   - APP_CRYPTO_KEY — this backend's own AES-256-GCM key (same one
//     internal/shared/crypto.FromEnv() already reads for every other
//     encrypt/decrypt call this backend makes).
//
// Idempotent and safe to re-run: a row that already decrypts under
// APP_CRYPTO_KEY (AES-GCM's authentication tag makes an accidental false
// "success" cryptographically infeasible) is left untouched — see
// classifyRow. Always run -dry-run first; it does every decrypt/re-encrypt
// step and reports would-be results without writing anything.
//
// Usage:
//
//	DJANGO_SECRET_KEY=... APP_CRYPTO_KEY=... go run ./cmd/reencrypt-credentials -dry-run
//	DJANGO_SECRET_KEY=... APP_CRYPTO_KEY=... go run ./cmd/reencrypt-credentials -apply
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/botspace/backend/internal/shared/config"
	"github.com/botspace/backend/internal/shared/crypto"
	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
)

type rowOutcome int

const (
	outcomeAlreadyAES rowOutcome = iota
	outcomeMigrated
	outcomeFailed
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run, decrypts/re-encrypts but writes nothing)")
	limit := flag.Int("limit", 0, "process at most N rows (0 = all) — useful for a first spot-check")
	flag.Parse()

	djangoSecret := os.Getenv("DJANGO_SECRET_KEY")
	if djangoSecret == "" {
		log.Fatal("DJANGO_SECRET_KEY not set — required to decrypt Fernet-encrypted rows")
	}
	fernetKey := crypto.DjangoFernetKey(djangoSecret)

	aesCipher, err := crypto.FromEnv()
	if err != nil {
		log.Fatalf("APP_CRYPTO_KEY: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config load: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := database.NewPool(ctx, cfg.DB)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	q := sqlcdb.New(pool)

	rows, err := q.ReencryptListAllCredentials(ctx)
	if err != nil {
		log.Fatalf("list credentials: %v", err)
	}
	if *limit > 0 && *limit < len(rows) {
		rows = rows[:*limit]
	}
	fmt.Printf("%d credential row(s) to check (dry-run=%v)\n", len(rows), !*apply)

	var alreadyAES, migrated, failed int
	var failedIDs []uuid.UUID

	for _, row := range rows {
		outcome, newEncrypted, classifyErr := classifyRow(fernetKey, aesCipher, row.DataEncrypted)
		switch outcome {
		case outcomeAlreadyAES:
			alreadyAES++
			continue
		case outcomeFailed:
			failed++
			failedIDs = append(failedIDs, row.ID)
			log.Printf("  FAILED  id=%s: neither AES-GCM (APP_CRYPTO_KEY) nor Fernet (DJANGO_SECRET_KEY) decrypt succeeded: %v", row.ID, classifyErr)
			continue
		case outcomeMigrated:
			migrated++
			if !*apply {
				fmt.Printf("  would migrate id=%s (Fernet -> AES-GCM)\n", row.ID)
				continue
			}
			affected, updErr := q.ReencryptUpdateCredentialData(ctx, sqlcdb.ReencryptUpdateCredentialDataParams{
				ID: row.ID, DataEncrypted: newEncrypted,
			})
			if updErr != nil {
				log.Printf("  WARN: update id=%s: %v", row.ID, updErr)
				migrated--
				failed++
				failedIDs = append(failedIDs, row.ID)
				continue
			}
			if affected == 0 {
				log.Printf("  WARN: update id=%s touched 0 rows (deleted concurrently?)", row.ID)
			}
			fmt.Printf("  migrated id=%s\n", row.ID)
		}
	}

	fmt.Printf("\ndone: total=%d already_aes=%d migrated=%d failed=%d", len(rows), alreadyAES, migrated, failed)
	if !*apply {
		fmt.Print(" (dry-run, nothing written)")
	}
	fmt.Println()
	if len(failedIDs) > 0 {
		fmt.Println("failed row ids (inspect manually — corrupt data or a key mismatch):")
		for _, id := range failedIDs {
			fmt.Printf("  %s\n", id)
		}
	}
}

// classifyRow determines whether dataEncrypted is already this backend's
// AES-GCM format (nothing to do), a Django Fernet token (decrypt + return
// the AES-GCM re-encryption of the same plaintext), or neither (failed —
// logged by id only, plaintext/token content is never logged).
func classifyRow(fernetKey [32]byte, aesCipher *crypto.Cipher, dataEncrypted string) (rowOutcome, string, error) {
	if plain, err := aesCipher.Decrypt(dataEncrypted); err == nil && json.Valid(plain) {
		return outcomeAlreadyAES, "", nil
	}

	plain, ferr := crypto.FernetDecrypt(fernetKey, dataEncrypted)
	if ferr != nil {
		return outcomeFailed, "", fmt.Errorf("fernet decrypt: %w", ferr)
	}
	if !json.Valid(plain) {
		return outcomeFailed, "", fmt.Errorf("fernet decrypt succeeded but payload is not valid JSON")
	}
	newEncrypted, eerr := aesCipher.Encrypt(plain)
	if eerr != nil {
		return outcomeFailed, "", fmt.Errorf("re-encrypt: %w", eerr)
	}
	return outcomeMigrated, newEncrypted, nil
}
