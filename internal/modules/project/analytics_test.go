package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type analyticsFake struct {
	values map[string]BotAnalytics
	err    error
}

func (f analyticsFake) ReadAnalytics(_ context.Context, ids []string, _ time.Time) (map[string]BotAnalytics, error) {
	out := map[string]BotAnalytics{}
	for _, id := range ids {
		out[id] = f.values[id]
	}
	return out, f.err
}

type analyticsProjectRepo struct{ *fakeRepo }

func (r analyticsProjectRepo) GetAdmin(_ context.Context, id uuid.UUID) (*AdminProjectDetail, error) {
	if p, ok := r.projects[id]; ok {
		return &AdminProjectDetail{Project: *p}, nil
	}
	return nil, database.ErrNotFound
}
func (r analyticsProjectRepo) CountAdmin(_ context.Context, _ AdminListFilter) (int64, error) {
	return int64(len(r.monitoring)), nil
}

func analyticsFixture() (Usecase, *fakeRepo, uuid.UUID) {
	repo := newFakeRepo()
	id := uuid.New()
	repo.projects[id] = &Project{ID: id, OwnerID: uuid.New()}
	repo.monitoring = []MonitoringProject{{Project: *repo.projects[id]}}
	uc := NewUsecaseWithAnalytics(NewUsecase(analyticsProjectRepo{repo}, nil, nil, nil, nil), analyticsFake{values: map[string]BotAnalytics{id.String(): {UsersTotal: 3, ActiveUsers24h: 1, ActiveUsers7d: 2, Messages24h: 4}}})
	return uc, repo, id
}

func TestAdminMonitoring_AnalyticsGlobalSorting(t *testing.T) {
	uc, repo, _ := analyticsFixture()
	values := map[string]BotAnalytics{}
	repo.monitoring = nil
	for i := 0; i < 10005; i++ {
		id := uuid.New()
		repo.monitoring = append(repo.monitoring, MonitoringProject{Project: Project{ID: id}})
		values[id.String()] = BotAnalytics{UsersTotal: int64(i), Messages24h: int64(10005 - i)}
	}
	NewUsecaseWithAnalytics(uc, analyticsFake{values: values})
	for _, tc := range []struct {
		order string
		users int64
	}{{"users_desc", 10003}, {"users_asc", 1}, {"activity_desc", 1}, {"activity_asc", 10003}} {
		t.Run(tc.order, func(t *testing.T) {
			page, err := uc.AdminMonitoring(context.Background(), AdminListFilter{Ordering: tc.order, Limit: 1, Offset: 1})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 10005 || !page.AnalyticsAvailable || len(page.Items) != 1 || page.Items[0].Analytics.UsersTotal != tc.users {
				t.Fatalf("bad page: %+v", page)
			}
		})
	}
}

func TestAdminMonitoring_AnalyticsFailure(t *testing.T) {
	uc, _, _ := analyticsFixture()
	NewUsecaseWithAnalytics(uc, analyticsFake{err: errors.New("mongo down")})
	page, err := uc.AdminMonitoring(context.Background(), AdminListFilter{})
	if err != nil || page.AnalyticsAvailable {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for _, order := range []string{"users_desc", "activity_asc", "invalid"} {
		if _, err := uc.AdminMonitoring(context.Background(), AdminListFilter{Ordering: order}); err == nil {
			t.Fatalf("%s should fail", order)
		}
	}
}

func TestAdminProjectStatistics_SuccessAndErrors(t *testing.T) {
	uc, _, id := analyticsFixture()
	result, err := uc.AdminProjectStatistics(context.Background(), id)
	if err != nil || result.UsersTotal != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := uc.AdminProjectStatistics(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected not found")
	}
	NewUsecaseWithAnalytics(uc, analyticsFake{err: errors.New("mongo down")})
	if _, err := uc.AdminProjectStatistics(context.Background(), id); err == nil {
		t.Fatal("expected mongo failure")
	}
}

func TestAdminBotStatistics_SuccessAndErrors(t *testing.T) {
	uc, repo, id := analyticsFixture()
	id2 := uuid.New()
	repo.monitoring = append(repo.monitoring, MonitoringProject{Project: Project{ID: id2}})
	NewUsecaseWithAnalytics(uc, analyticsFake{values: map[string]BotAnalytics{id.String(): {UsersTotal: 3, Messages24h: 4}, id2.String(): {UsersTotal: 5, Messages24h: 2}}})
	out, err := uc.AdminBotStatistics(context.Background(), 1, "users_desc")
	if err != nil {
		t.Fatal(err)
	}
	if out.ProjectsTotal != 2 || out.Totals.UsersTotal != 8 || out.Totals.Messages24h != 6 || len(out.TopBots) != 1 || out.TopBots[0].ID != id2 {
		t.Fatalf("bad totals/ranking: %+v", out)
	}
	if _, err := uc.AdminBotStatistics(context.Background(), 1, "users_asc"); err == nil {
		t.Fatal("expected validation error")
	}
	NewUsecaseWithAnalytics(uc, analyticsFake{err: errors.New("mongo down")})
	if _, err := uc.AdminBotStatistics(context.Background(), 1, ""); err == nil {
		t.Fatal("expected mongo failure")
	}
}

type analyticsVerifier struct{ owner string }

func (v analyticsVerifier) VerifyAccessToken(token string) (string, string, error) {
	return v.owner, token, nil
}

func TestAnalyticsHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc, repo, id := analyticsFixture()
	uc = NewUsecaseWithMetrics(uc, analyticsHistoryProvider{})
	r := gin.New()
	RegisterRoutes(r.Group("/api/v1"), NewHandler(uc), analyticsVerifier{owner: repo.projects[id].OwnerID.String()})
	for _, tc := range []struct {
		path, role string
		status     int
		contains   string
	}{
		{"/admin/monitoring?ordering=users_desc", "admin", 200, `"users_total":3`},
		{"/admin/monitoring?ordering=bad", "admin", 400, `request.invalid`},
		{"/admin/projects/" + id.String() + "/statistics", "admin", 200, `"messages_24h":4`},
		{"/admin/projects/" + uuid.NewString() + "/statistics", "admin", 404, `project.not_found`},
		{"/admin/projects/bad/statistics", "admin", 400, `error`},
		{"/admin/stats/bots", "admin", 200, `"projects_total":1`},
		{"/admin/stats/bots?ordering=bad", "admin", 400, `request.invalid`},
		{"/projects/" + id.String() + "/monitoring", "user", 200, `"analytics_available":true`},
		{"/projects/" + uuid.NewString() + "/monitoring", "user", 404, `project.not_found`},
		{"/projects/" + id.String() + "/monitoring/history", "user", 200, `"resource_metrics_available":true`},
		{"/projects/" + uuid.NewString() + "/monitoring/history", "user", 404, `project.not_found`},
		{"/admin/monitoring/" + id.String() + "/history", "admin", 200, `"resource_metrics_available":true`},
		{"/admin/monitoring/bad/history", "admin", 400, `error`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1"+tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+tc.role)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
	for _, path := range []string{"/admin/monitoring", "/admin/stats/bots", "/admin/projects/" + id.String() + "/statistics"} {
		for _, role := range []string{"", "user"} {
			req := httptest.NewRequest("GET", "/api/v1"+path, nil)
			if role != "" {
				req.Header.Set("Authorization", "Bearer "+role)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			want := 401
			if role != "" {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("%s role=%s got %d", path, role, w.Code)
			}
		}
	}
	NewUsecaseWithAnalytics(uc, analyticsFake{err: errors.New("private mongo failure")})
	for _, path := range []string{"/admin/monitoring?ordering=users_desc", "/admin/stats/bots", "/admin/projects/" + id.String() + "/statistics"} {
		req := httptest.NewRequest("GET", "/api/v1"+path, nil)
		req.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 500 || strings.Contains(w.Body.String(), "private mongo") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

type analyticsHistoryProvider struct{}

func (analyticsHistoryProvider) ProjectMetrics(context.Context) (map[uuid.UUID]LiveMetrics, bool, error) {
	return nil, true, nil
}

func (analyticsHistoryProvider) ProjectHistory(context.Context, uuid.UUID, string) (*MonitoringHistory, bool, error) {
	return &MonitoringHistory{ResourceMetricsAvailable: true, CPU: []MetricPoint{{Timestamp: 1, Value: 0.25}}, Memory: []MetricPoint{{Timestamp: 1, Value: 1024}}}, true, nil
}

func TestMonitoringHistory_AttributionAndOwnership(t *testing.T) {
	uc, repo, id := analyticsFixture()
	uc = NewUsecaseWithMetrics(uc, analyticsHistoryProvider{})
	h, err := uc.MonitoringHistory(context.Background(), id, repo.projects[id].OwnerID, "1h")
	if err != nil || !h.ResourceMetricsAvailable || !h.PrometheusAvailable {
		t.Fatalf("history=%+v err=%v", h, err)
	}
	if _, err := uc.MonitoringHistory(context.Background(), id, uuid.New(), "1h"); err == nil {
		t.Fatal("ownership check missing")
	}
	h, err = uc.AdminMonitoringHistory(context.Background(), id, "1h")
	if err != nil || !h.ResourceMetricsAvailable {
		t.Fatalf("history=%+v err=%v", h, err)
	}
	uc = NewUsecaseWithMetrics(uc, nil)
	h, err = uc.AdminMonitoringHistory(context.Background(), id, "1h")
	if err != nil || h.ResourceMetricsAvailable {
		t.Fatalf("unavailable history=%+v err=%v", h, err)
	}
}

func TestMonitoringPaginationAndTies(t *testing.T) {
	a, b := uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002")
	items := []MonitoringProject{{Project: Project{ID: b}}, {Project: Project{ID: a}}}
	sortMonitoringByResource(items, "activity_desc")
	if items[0].ID != a {
		t.Fatal("ties not deterministic")
	}
	out := toMonitoringListResponse(&MonitoringPage{}, 0, -10)
	if out.Limit != 20 || out.Offset != 0 {
		t.Fatalf("bad pagination %+v", out)
	}
}

func TestPoolPrometheusQueriesAndEmptySamples(t *testing.T) {
	id := uuid.New()
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query().Get("query")
				if !strings.Contains(q, `namespace="test-pool"`) || strings.Contains(q, `pod=~"engine-pool`) {
					t.Errorf("bad attribution/namespace: %s", q)
				}
				if strings.Contains(q, "container_cpu") && !strings.Contains(q, "engine_pool_runtime_cpu_seconds_total") {
					t.Errorf("missing pool CPU: %s", q)
				}
				if strings.Contains(q, "container_memory") && !strings.Contains(q, "engine_pool_runtime_memory_rss_bytes") {
					t.Errorf("missing pool memory: %s", q)
				}
				if empty {
					fmt.Fprint(w, `{"status":"success","data":{"result":[]}}`)
					return
				}
				if r.URL.Path == "/api/v1/query_range" {
					fmt.Fprint(w, `{"status":"success","data":{"result":[{"values":[[1,"0.25"]]}]}}`)
					return
				}
				fmt.Fprintf(w, `{"status":"success","data":{"result":[{"metric":{"project_id":"%s"},"value":[1,"0.25"]}]}}`, id)
			}))
			defer srv.Close()
			p := NewPrometheusMetricsProvider(srv.URL, "test-pool")
			values, ok, err := p.ProjectMetrics(context.Background())
			if err != nil || !ok || (len(values) == 0) != empty {
				t.Fatalf("values=%v available=%v err=%v", values, ok, err)
			}
			history, ok, err := p.(MetricsHistoryProvider).ProjectHistory(context.Background(), id, "1h")
			if err != nil || !ok || history.ResourceMetricsAvailable == empty {
				t.Fatalf("history=%+v err=%v", history, err)
			}
		})
	}
}

func TestAnalyticsResponse_ZeroLastActivityIsNull(t *testing.T) {
	raw, err := json.Marshal(toBotAnalyticsResponse(BotAnalytics{}))
	if err != nil || !strings.Contains(string(raw), `"last_activity_at":null`) {
		t.Fatalf("%s %v", raw, err)
	}
}
