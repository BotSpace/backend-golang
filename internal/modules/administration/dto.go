package administration

import (
	"encoding/json"
	"time"
)

// AuditLogResponse is the public projection of an audit log entry.
type AuditLogResponse struct {
	ID        string          `json:"id"`
	ActorID   *string         `json:"actor_id"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	IP        string          `json:"ip,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

func toAuditLogResponse(a *AuditLog) AuditLogResponse {
	var actorID *string
	if a.ActorID != nil {
		s := a.ActorID.String()
		actorID = &s
	}
	return AuditLogResponse{
		ID:        a.ID.String(),
		ActorID:   actorID,
		Action:    a.Action,
		Target:    a.Target,
		Metadata:  a.Metadata,
		IP:        a.IP,
		CreatedAt: a.CreatedAt,
	}
}

// AuditLogListResponse is a paginated envelope for an audit log listing.
type AuditLogListResponse struct {
	Items  []AuditLogResponse `json:"items"`
	Total  int64              `json:"total"`
	Limit  int32              `json:"limit"`
	Offset int32              `json:"offset"`
}

func toAuditLogListResponse(items []AuditLog, total int64, limit, offset int32) AuditLogListResponse {
	out := make([]AuditLogResponse, len(items))
	for i := range items {
		out[i] = toAuditLogResponse(&items[i])
	}
	return AuditLogListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// DailyCountResponse is one point of a growth chart.
type DailyCountResponse struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// DashboardStatsResponse mirrors the legacy /admin/stats/dashboard payload.
// Users/projects come straight from this module's tables; templates/
// withdrawals/revenue are computed via read-only cross-module SQL (see
// DashboardStats's doc comment in domain.go).
type DashboardStatsResponse struct {
	Users struct {
		Total    int64 `json:"total"`
		Active   int64 `json:"active"`
		NewToday int64 `json:"new_today"`
		New7d    int64 `json:"new_7d"`
		New30d   int64 `json:"new_30d"`
	} `json:"users"`
	Projects struct {
		Total       int64 `json:"total"`
		WithRelease int64 `json:"with_release"`
	} `json:"projects"`
	Templates struct {
		Total         int64 `json:"total"`
		PendingVerify int64 `json:"pending_verify"`
	} `json:"templates"`
	Withdrawals struct {
		Pending int64 `json:"pending"`
	} `json:"withdrawals"`
	Revenue30d   string               `json:"revenue_30d"`
	DailySignups []DailyCountResponse `json:"daily_signups"`
	DailyOrders  []DailyCountResponse `json:"daily_orders"`
}

func toDashboardStatsResponse(s *DashboardStats) DashboardStatsResponse {
	var resp DashboardStatsResponse
	resp.Users.Total = s.UsersTotal
	resp.Users.Active = s.UsersActive
	resp.Users.NewToday = s.UsersNewToday
	resp.Users.New7d = s.UsersNew7d
	resp.Users.New30d = s.UsersNew30d
	resp.Projects.Total = s.ProjectsTotal
	resp.Projects.WithRelease = s.ProjectsWithRelease
	resp.Templates.Total = s.TemplatesTotal
	resp.Templates.PendingVerify = s.TemplatesPendingVerify
	resp.Withdrawals.Pending = s.WithdrawalsPending
	resp.Revenue30d = s.Revenue30d
	resp.DailySignups = make([]DailyCountResponse, len(s.DailySignups))
	for i, d := range s.DailySignups {
		resp.DailySignups[i] = DailyCountResponse{Day: d.Day, Count: d.Count}
	}
	resp.DailyOrders = make([]DailyCountResponse, len(s.DailyOrders))
	for i, d := range s.DailyOrders {
		resp.DailyOrders[i] = DailyCountResponse{Day: d.Day, Count: d.Count}
	}
	return resp
}
