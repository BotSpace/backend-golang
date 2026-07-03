package template

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// This file ports core/apps/template/views/internal.py: two X-Internal-Token
// gated, unauthenticated-otherwise endpoints the agent's RAG indexer polls to
// embed every template's flow JSON (public catalog endpoints intentionally
// hide `flow` — payload size and IP protection). See routes.go for the
// X-Internal-Token check (same pattern as internal/modules/credential's
// internalTokenAuth()).

// FeedItem is the wire shape for GET /template/internal/list/ and
// GET /template/internal/{id}/ — mirrors Django's _serialize_template()
// field-for-field.
type FeedItem struct {
	ID               string           `json:"id"`
	Slug             string           `json:"slug"`
	Name             string           `json:"name"`
	ShortDescription string           `json:"short_description"`
	Description      string           `json:"description"`
	Categories       []FeedTag        `json:"categories"`
	Features         []FeedFeatureTag `json:"features"`
	Flow             json.RawMessage  `json:"flow"`
	IsVerify         bool             `json:"is_verify"`
	RequiredEnvs     json.RawMessage  `json:"required_envs"`
}

type FeedTag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type FeedFeatureTag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FeedListResponse wraps the list endpoint: {"results": [...]}.
type FeedListResponse struct {
	Results []FeedItem `json:"results"`
}

// ListInternalFeed ports list_templates_with_flow: every template (all
// verification states unless onlyVerified), each carrying its linked
// project's flow JSON.
func (u *usecase) ListInternalFeed(ctx context.Context, onlyVerified bool) ([]FeedItem, error) {
	filter := AdminTemplateListFilter{Limit: 100000, Offset: 0}
	if onlyVerified {
		v := true
		filter.IsVerify = &v
	}
	summaries, err := u.repo.ListAdmin(ctx, filter)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	items := make([]Template, len(summaries))
	for i := range summaries {
		items[i] = summaries[i].Template
	}
	if err := u.repo.AttachExtras(ctx, items, nil); err != nil {
		return nil, httpx.Internal(err)
	}
	out := make([]FeedItem, len(items))
	for i := range items {
		out[i] = u.toFeedItem(ctx, &items[i])
	}
	return out, nil
}

// GetInternalFeedItem ports get_template_with_flow: identifier is tried as a
// UUID (this table's primary key) first, then as a slug — with NO
// is_verify restriction either way (matches Django's unscoped queryset).
func (u *usecase) GetInternalFeedItem(ctx context.Context, identifier string) (*FeedItem, error) {
	var t *Template
	var err error
	if id, parseErr := uuid.Parse(identifier); parseErr == nil {
		t, err = u.repo.GetByID(ctx, id)
	} else {
		t, err = u.repo.GetBySlugAny(ctx, identifier)
	}
	if err != nil {
		return nil, mapNoRowsHTTP(err)
	}
	items := []Template{*t}
	if err := u.repo.AttachExtras(ctx, items, nil); err != nil {
		return nil, httpx.Internal(err)
	}
	item := u.toFeedItem(ctx, &items[0])
	return &item, nil
}

// toFeedItem resolves the linked project's flow (or the {"nodes":[],
// "edges":[]} default when there is none / the lookup fails — a broken
// project link must never break the whole RAG feed) and builds the wire DTO.
func (u *usecase) toFeedItem(ctx context.Context, t *Template) FeedItem {
	flow := normalizeFlow(nil)
	if t.ProjectID != nil {
		if raw, err := u.repo.GetProjectFlow(ctx, *t.ProjectID); err == nil {
			flow = normalizeFlow(raw)
		}
	}
	cats := make([]FeedTag, len(t.Categories))
	for i, c := range t.Categories {
		cats[i] = FeedTag{ID: c.ID.String(), Name: c.Name}
	}
	feats := make([]FeedFeatureTag, len(t.Features))
	for i, f := range t.Features {
		feats[i] = FeedFeatureTag{ID: f.ID.String(), Name: f.Name, Value: f.Value}
	}
	requiredEnvs := t.RequiredEnvs
	if len(requiredEnvs) == 0 {
		requiredEnvs = json.RawMessage("[]")
	}
	return FeedItem{
		ID:               t.ID.String(),
		Slug:             t.Slug,
		Name:             t.Name,
		ShortDescription: t.ShortDescription,
		Description:      t.Description,
		Categories:       cats,
		Features:         feats,
		Flow:             flow,
		IsVerify:         t.IsVerify,
		RequiredEnvs:     requiredEnvs,
	}
}

// normalizeFlow mirrors _serialize_template's flow normalisation: a dict
// (JSON object) passes through as-is; a bare list (older format, flat node
// array) is wrapped as {"nodes": raw, "edges": []}; anything empty/invalid
// falls back to the empty-graph default.
func normalizeFlow(raw json.RawMessage) json.RawMessage {
	trimmed := skipJSONWhitespace(raw)
	switch {
	case len(trimmed) == 0:
		return json.RawMessage(`{"nodes":[],"edges":[]}`)
	case trimmed[0] == '{':
		return raw
	case trimmed[0] == '[':
		wrapped, err := json.Marshal(map[string]json.RawMessage{
			"nodes": raw,
			"edges": json.RawMessage("[]"),
		})
		if err != nil {
			return json.RawMessage(`{"nodes":[],"edges":[]}`)
		}
		return wrapped
	default:
		return json.RawMessage(`{"nodes":[],"edges":[]}`)
	}
}

func skipJSONWhitespace(raw json.RawMessage) json.RawMessage {
	i := 0
	for i < len(raw) {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return raw[i:]
		}
	}
	return raw[i:]
}

func mapNoRowsHTTP(err error) error {
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("template.not_found", "template not found")
	}
	return httpx.Internal(err)
}
