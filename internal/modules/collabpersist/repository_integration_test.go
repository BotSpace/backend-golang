//go:build integration

package collabpersist

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResolveRuntimeProject_AuthoritativeSQLMapping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-d", "-p", "127.0.0.1::5432", "-e", "POSTGRES_PASSWORD=test-only", "postgres:16-alpine")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("start test Postgres: %v %s", err, stderr.String())
	}
	container := strings.TrimSpace(string(raw))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", container).CombinedOutput(); err != nil {
			t.Errorf("remove Postgres: %v %s", err, out)
		}
	})
	raw, err = exec.CommandContext(ctx, "docker", "port", container, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, "postgres://postgres:test-only@"+strings.TrimSpace(string(raw))+"/postgres?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for pool.Ping(ctx) != nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Apply the real migrations defining the tables used by these existing sqlc queries.
	for _, path := range []string{"../../../migrations/000001_create_users.up.sql", "../../../migrations/000004_create_projects.up.sql", "../../../migrations/000033_cms_legacy_project_id.up.sql"} {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	owner, id, public, debug, release := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO users (id) VALUES ($1)", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO projects (id,uuid,owner_id,name,legacy_id,debug_webhook_uuid,release_webhook_uuid) VALUES ($1,$2,$3,'legacy',123,$4,$5)", id, public, owner, debug, release); err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool).(RuntimeIdentityRepository)
	for _, tc := range []struct {
		ref, hook string
		debug     bool
		valid     bool
	}{
		{"123", debug.String(), true, true},
		{"123", release.String(), false, true},
		{id.String(), debug.String(), true, true},
		{public.String(), debug.String(), true, false},
		{"124", debug.String(), true, false},
		{"123", release.String(), true, false},
		{"123", uuid.NewString(), false, false},
		{"invalid", debug.String(), true, false},
	} {
		got, err := repo.ResolveRuntimeProject(ctx, tc.ref, tc.hook, tc.debug)
		if tc.valid && (err != nil || got != id.String()) {
			t.Fatalf("mapping %q: got=%s err=%v", tc.ref, got, err)
		}
		if !tc.valid && err == nil {
			t.Fatalf("unverified mapping accepted for %q", tc.ref)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE projects SET deleted_at=now() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveRuntimeProject(ctx, "123", debug.String(), true); err == nil {
		t.Fatal("deleted legacy project accepted")
	}
	if _, err := repo.ResolveRuntimeProject(ctx, id.String(), debug.String(), true); err == nil {
		t.Fatal("deleted UUID project accepted")
	}
}
