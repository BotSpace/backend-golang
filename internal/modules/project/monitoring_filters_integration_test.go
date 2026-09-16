//go:build integration

package project

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func monitoringTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "docker", "run", "--rm", "-d", "-e", "POSTGRES_PASSWORD=test", "-p", "127.0.0.1::5432", "postgres:16-alpine").CombinedOutput()
	if err != nil {
		t.Fatalf("start Postgres: %v %s", err, raw)
	}
	container := strings.TrimSpace(string(raw))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", container).CombinedOutput(); err != nil {
			t.Errorf("cleanup Postgres: %v %s", err, out)
		}
	})
	raw, err = exec.CommandContext(ctx, "docker", "port", container, "5432/tcp").CombinedOutput()
	if err != nil {
		t.Fatalf("Postgres port: %v %s", err, raw)
	}
	dsn := "postgres://postgres:test@" + strings.TrimSpace(string(raw)) + "/postgres?sslmode=disable"
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for pool.Ping(ctx) != nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := database.MigrateUp(dsn, "file://../../../migrations"); err != nil {
		t.Fatal(err)
	}
	// Migrations seed demo projects; isolate fixtures in this disposable database.
	if _, err := pool.Exec(ctx, "TRUNCATE projects CASCADE"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestMonitoringRepository_SQLFilters(t *testing.T) {
	pool := monitoringTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	execSQL := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	owner, other := uuid.New(), uuid.New()
	execSQL("INSERT INTO users (id) VALUES ($1), ($2)", owner, other)
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := day.AddDate(0, 0, 1)
	type seed struct {
		owner             uuid.UUID
		template, release bool
		at                time.Time
		name              string
		deleted           bool
	}
	seeds := []seed{
		{owner, true, true, day, "match", false},
		{owner, true, true, end.Add(-time.Microsecond), "MATCH late", false},
		{other, true, true, day, "match other owner", false},
		{owner, false, true, day, "match not template", false},
		{owner, true, false, day, "match no release", false},
		{owner, true, true, day.Add(-time.Microsecond), "match too early", false},
		{owner, true, true, end, "match next midnight", false},
		{owner, true, true, day, "different", false},
		{owner, true, true, day, "match deleted", true},
	}
	ids := make([]uuid.UUID, len(seeds))
	for i, s := range seeds {
		ids[i] = uuid.New()
		execSQL("INSERT INTO projects (id,owner_id,name,is_template,created_at) VALUES ($1,$2,$3,$4,$5)", ids[i], s.owner, s.name, s.template, s.at)
		if s.release {
			// Multiple releases must not duplicate rows or inflate counts; stopped still counts.
			execSQL("INSERT INTO relase (project_id,version,status) VALUES ($1,1,'failed'), ($1,2,'stopped')", ids[i])
		}
		if s.deleted {
			execSQL("UPDATE projects SET deleted_at=NOW() WHERE id=$1", ids[i])
		}
	}
	repo := NewPostgresRepository(pool)
	yes, no := true, false
	cases := []struct {
		name  string
		f     AdminListFilter
		total int64
	}{
		{"all", AdminListFilter{}, 8},
		{"owner", AdminListFilter{OwnerID: &owner}, 7},
		{"template", AdminListFilter{IsTemplate: &yes}, 7},
		{"not template", AdminListFilter{IsTemplate: &no}, 1},
		{"release", AdminListFilter{HasRelease: &yes}, 7},
		{"no release", AdminListFilter{HasRelease: &no}, 1},
		{"after", AdminListFilter{CreatedAfter: &day}, 7},
		{"before", AdminListFilter{CreatedBefore: &end}, 7},
		{"search", AdminListFilter{Search: "match"}, 7},
		{"combined", AdminListFilter{OwnerID: &owner, IsTemplate: &yes, HasRelease: &yes, CreatedAfter: &day, CreatedBefore: &end, Search: "match"}, 2},
		{"no matches", AdminListFilter{Search: "absent"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			f.Limit = 100
			total, err := repo.CountAdmin(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := repo.ListMonitoring(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			admin, err := repo.ListAdmin(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			if total != tc.total || int64(len(rows)) != total || len(admin) != len(rows) {
				t.Fatalf("count=%d rows=%d admin=%d want %d", total, len(rows), len(admin), tc.total)
			}
			set := map[uuid.UUID]bool{}
			for _, p := range admin {
				set[p.ID] = true
			}
			for _, p := range rows {
				if !set[p.ID] {
					t.Fatalf("lists disagree on %s", p.ID)
				}
			}
		})
	}
	f := cases[9].f
	for _, order := range []string{"", "users_desc", "activity_asc"} {
		uc := NewUsecaseWithAnalytics(NewUsecase(repo, nil, nil, nil, nil), analyticsFake{values: map[string]BotAnalytics{ids[0].String(): {UsersTotal: 100, Messages24h: 1}, ids[1].String(): {UsersTotal: 1, Messages24h: 100}}})
		for offset := int32(0); offset < 3; offset++ {
			t.Run(fmt.Sprintf("page/%s/%d", order, offset), func(t *testing.T) {
				f.Ordering, f.Limit, f.Offset = order, 1, offset
				page, err := uc.AdminMonitoring(ctx, f)
				if err != nil {
					t.Fatal(err)
				}
				if page.Total != 2 {
					t.Fatalf("total=%d", page.Total)
				}
				if offset == 2 {
					if len(page.Items) != 0 {
						t.Fatal("expected empty page")
					}
					return
				}
				index := int(offset)
				if order == "" {
					index = 1 - index
				}
				if len(page.Items) != 1 || page.Items[0].ID != ids[index] {
					t.Fatalf("wrong page: %+v", page.Items)
				}
			})
		}
	}
}
