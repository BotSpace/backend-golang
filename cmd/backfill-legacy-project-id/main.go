// Command backfill-legacy-project-id is a one-shot data migration
// (see migrations/000033_cms_legacy_project_id + internal/modules/cms/usecase.go
// resolveMongoTenant): it recovers, for every `projects` row imported by
// scripts/etl_django.py, the original Django `project` table integer primary
// key and stores it in the new `projects.legacy_id` column.
//
// WHY this is needed: real production CMS documents in the shared Mongo
// database (botspace_cms) are tagged with Django's `_project_id`, which is
// that integer pk — not this table's UUID `id`/`uuid` columns. The ETL
// derived `projects.id` as uuid5(NS, "project:<django_pk>") — a one-way
// hash, so the pk cannot be recovered from the row itself. This command
// brute-forces the same hash over a pk range and matches it back to
// projects.id, the only way to reverse a uuid5 without the source database.
//
// Usage:
//
//	go run ./cmd/backfill-legacy-project-id -dry-run       # count matches, write nothing
//	go run ./cmd/backfill-legacy-project-id -max-pk 50000   # override the search range
//	go run ./cmd/backfill-legacy-project-id                 # apply
//
// Idempotent: CmsSetProjectLegacyID only writes rows where legacy_id IS NULL,
// and this command only queries/updates projects still missing one, so
// re-running is always safe.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	"github.com/botspace/backend/internal/shared/config"
	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
)

// namespace mirrors scripts/etl_django.py's NS = uuid.UUID("6ba7b810-9dad-11d1-80b4-00c04fd430c8").
var namespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

func main() {
	dryRun := flag.Bool("dry-run", false, "report matches without writing")
	maxPK := flag.Int("max-pk", 50000, "highest Django project pk to try (observed prod range: ~985-1408)")
	flag.Parse()

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

	ids, err := q.CmsListProjectIDsWithoutLegacyID(ctx)
	if err != nil {
		log.Fatalf("list projects without legacy_id: %v", err)
	}
	if len(ids) == 0 {
		fmt.Println("nothing to do: every project already has a legacy_id")
		return
	}
	fmt.Printf("%d project(s) missing legacy_id; searching pk range 1..%d\n", len(ids), *maxPK)

	want := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}

	matched := 0
	for pk := 1; pk <= *maxPK && len(want) > 0; pk++ {
		candidate := uuid.NewSHA1(namespace, []byte(fmt.Sprintf("project:%d", pk)))
		if _, ok := want[candidate]; !ok {
			continue
		}
		matched++
		delete(want, candidate)
		fmt.Printf("  matched projects.id=%s -> legacy_id=%d\n", candidate, pk)
		if *dryRun {
			continue
		}
		rows, err := q.CmsSetProjectLegacyID(ctx, sqlcdb.CmsSetProjectLegacyIDParams{
			ID:       candidate,
			LegacyID: int32Ptr(int32(pk)),
		})
		if err != nil {
			log.Printf("  WARN: set legacy_id for %s: %v", candidate, err)
			continue
		}
		if rows == 0 {
			log.Printf("  WARN: %s matched pk=%d but update touched 0 rows (already set concurrently?)", candidate, pk)
		}
	}

	fmt.Printf("\ndone: %d/%d matched", matched, len(ids))
	if *dryRun {
		fmt.Print(" (dry-run, nothing written)")
	}
	fmt.Println()
	if len(want) > 0 {
		fmt.Printf("unmatched (%d): no pk in 1..%d hashed to these ids — they are either\n"+
			"post-cutover projects (created directly in Go, never existed in Django; expected)\n"+
			"or their Django pk exceeds -max-pk (rerun with a higher value):\n", len(want), *maxPK)
		for id := range want {
			fmt.Printf("  %s\n", id)
		}
	}
}

func int32Ptr(v int32) *int32 { return &v }
