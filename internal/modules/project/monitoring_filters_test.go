package project

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type filteredMonitoringRepo struct {
	*fakeRepo
	expected AdminListFilter
	t        *testing.T
}

func (r filteredMonitoringRepo) matches(f AdminListFilter) []MonitoringProject {
	r.t.Helper()
	got, want := f, r.expected
	got.Limit, got.Offset, got.Ordering = 0, 0, ""
	want.Limit, want.Offset, want.Ordering = 0, 0, ""
	if !reflect.DeepEqual(got, want) {
		r.t.Fatalf("filters lost: got %+v want %+v", got, want)
	}
	var items []MonitoringProject
	for _, p := range r.monitoring {
		if f.OwnerID != nil && p.OwnerID != *f.OwnerID {
			continue
		}
		if f.IsTemplate != nil && p.IsTemplate != *f.IsTemplate {
			continue
		}
		if f.HasRelease != nil && (p.ReleaseStatus != "") != *f.HasRelease {
			continue
		}
		if f.CreatedAfter != nil && p.CreatedAt.Before(*f.CreatedAfter) {
			continue
		}
		if f.CreatedBefore != nil && !p.CreatedAt.Before(*f.CreatedBefore) {
			continue
		}
		if !strings.Contains(strings.ToLower(p.Name), strings.ToLower(f.Search)) {
			continue
		}
		items = append(items, p)
	}
	return items
}
func (r filteredMonitoringRepo) ListMonitoring(_ context.Context, f AdminListFilter) ([]MonitoringProject, error) {
	return append([]MonitoringProject{}, pageSlice(r.matches(f), f.Offset, f.Limit)...), nil
}
func (r filteredMonitoringRepo) CountAdmin(_ context.Context, f AdminListFilter) (int64, error) {
	return int64(len(r.matches(f))), nil
}

func TestAdminMonitoring_FilteredCountSortPage(t *testing.T) {
	owner := uuid.New()
	yes := true
	after := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	before := after.AddDate(0, 0, 1)
	f := AdminListFilter{OwnerID: &owner, Search: "match", IsTemplate: &yes, HasRelease: &yes, CreatedAfter: &after, CreatedBefore: &before}
	repo := newFakeRepo()
	values := map[string]BotAnalytics{}
	// Interleave higher-ranked nonmatches. The matching set also spans SQL batches.
	count := int(monitoringBatchSize) + 3
	for i := 0; i < count; i++ {
		p := MonitoringProject{Project: Project{ID: uuid.New(), OwnerID: owner, Name: "match", IsTemplate: true, CreatedAt: after}, ReleaseStatus: "stopped", DiskBytes: int64(i)}
		values[p.ID.String()] = BotAnalytics{UsersTotal: int64(i), Messages24h: int64(i)}
		repo.monitoring = append(repo.monitoring, p)
		p.ID, p.OwnerID = uuid.New(), uuid.New()
		values[p.ID.String()] = BotAnalytics{UsersTotal: 999999, Messages24h: 999999}
		repo.monitoring = append(repo.monitoring, p)
	}
	uc := NewUsecaseWithAnalytics(NewUsecase(filteredMonitoringRepo{repo, f, t}, nil, nil, nil, nil), analyticsFake{values: values})
	for _, order := range []string{"", "users_desc", "activity_asc", "disk_desc"} {
		for _, offset := range []int32{0, 1, int32(count - 1), int32(count + 1)} {
			t.Run(fmt.Sprintf("%s/%d", order, offset), func(t *testing.T) {
				f.Ordering, f.Limit, f.Offset = order, 1, offset
				page, err := uc.AdminMonitoring(context.Background(), f)
				if err != nil {
					t.Fatal(err)
				}
				if page.Total != int64(count) {
					t.Fatalf("total=%d want %d", page.Total, count)
				}
				if int(offset) >= count {
					if len(page.Items) != 0 {
						t.Fatal("expected empty page")
					}
					return
				}
				index := int(offset)
				if strings.HasSuffix(order, "_desc") {
					index = count - 1 - index
				}
				if len(page.Items) != 1 || page.Items[0].ID != repo.monitoring[index*2].ID {
					t.Fatalf("wrong filtered page: %+v", page.Items)
				}
			})
		}
	}
}

func TestAdminFilters_Handlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc, _, id := analyticsFixture()
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), NewHandler(uc), analyticsVerifier{owner: id.String()})
	for _, endpoint := range []string{"/admin/monitoring", "/admin/projects"} {
		for _, query := range []string{"owner=nope", "owner=", "is_template=maybe", "has_release=2", "is_template=", "created_after=2026-02-30", "created_before=2026-09-01T00:00:00Z", "created_after=2026-09-02&created_before=2026-09-01", "limit=abc", "offset=-1", "offset=2147483648"} {
			t.Run(endpoint+"?"+query, func(t *testing.T) {
				req := httptest.NewRequest("GET", "/api/v1"+endpoint+"?"+query, nil)
				req.Header.Set("Authorization", "Bearer admin")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != 400 || !strings.Contains(w.Body.String(), "request.invalid") {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestAdminMonitoring_FilteredHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner := uuid.New()
	no := false
	after := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	before := after.AddDate(0, 0, 1)
	f := AdminListFilter{OwnerID: &owner, Search: "bot", IsTemplate: &no, HasRelease: &no, CreatedAfter: &after, CreatedBefore: &before}
	repo := newFakeRepo()
	for i := 0; i < 3; i++ {
		repo.monitoring = append(repo.monitoring, MonitoringProject{Project: Project{ID: uuid.New(), OwnerID: owner, Name: "bot", CreatedAt: after}, DiskBytes: int64(i)})
	}
	uc := NewUsecase(filteredMonitoringRepo{repo, f, t}, nil, nil, nil, nil)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), NewHandler(uc), analyticsVerifier{owner: owner.String()})
	req := httptest.NewRequest("GET", "/api/v1/admin/monitoring?owner="+owner.String()+"&search=bot&is_template=false&has_release=false&created_after=2026-09-01&created_before=2026-09-01&ordering=disk_desc&limit=1&offset=1", nil)
	req.Header.Set("Authorization", "Bearer admin")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var p MonitoringListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Total != 3 || p.Limit != 1 || p.Offset != 1 || len(p.Items) != 1 || p.Items[0].ID != repo.monitoring[1].ID.String() {
		t.Fatalf("wrong response: %s", w.Body.String())
	}
}
