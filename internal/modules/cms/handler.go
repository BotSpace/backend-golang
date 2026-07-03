package cms

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc Usecase
}

func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// forbiddenMongoOperators blocks server-side-JS / cross-collection operators
// from a caller-supplied filter or patch (NoSQL injection guard), mirroring
// Django's _FORBIDDEN_MONGO_OPERATORS.
var forbiddenMongoOperators = map[string]struct{}{
	"$where": {}, "$function": {}, "$accumulator": {}, "$expr": {}, "$merge": {}, "$out": {},
}

func findForbiddenOperator(node any, depth int) string {
	if depth > 12 {
		return "$depth"
	}
	switch v := node.(type) {
	case map[string]any:
		for k, val := range v {
			if _, bad := forbiddenMongoOperators[k]; bad {
				return k
			}
			if found := findForbiddenOperator(val, depth+1); found != "" {
				return found
			}
		}
	case []any:
		for _, e := range v {
			if found := findForbiddenOperator(e, depth+1); found != "" {
				return found
			}
		}
	}
	return ""
}

// owner extracts the authenticated user id, writing a 401 on failure.
func owner(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	return id, true
}

func pathProjectID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("project_id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("cms.invalid_project_id", "invalid project id"))
		return uuid.Nil, false
	}
	return id, true
}

// writeUcErr renders a usecase error, unwrapping *DocValidationError into a
// 400 with per-field details instead of a generic 500.
func writeUcErr(c *gin.Context, err error) {
	var ve *DocValidationError
	if errors.As(err, &ve) {
		httpx.ErrorWithDetails(c, httpx.BadRequest("cms.validation_failed", "document validation failed"), validationDetails(ve.Fields))
		return
	}
	httpx.Error(c, err)
}

// ── Collection ───────────────────────────────────────────────────────────

// ListCollections godoc
// @Summary  List a project's CMS collections
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections [get]
func (h *Handler) ListCollections(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	items, err := h.uc.ListCollections(c.Request.Context(), ownerID, projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, gin.H{"items": toCollectionListResponse(items)})
}

// CreateCollection godoc
// @Summary  Create a CMS collection (schema)
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections [post]
func (h *Handler) CreateCollection(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	var req CreateCollectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	coll, err := h.uc.CreateCollection(c.Request.Context(), ownerID, projectID, CreateCollectionInput{
		Name:         req.Name,
		Slug:         req.Slug,
		Description:  req.Description,
		PrimaryField: req.PrimaryField,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toCollectionResponse(coll))
}

// GetCollection godoc
// @Summary  Get a CMS collection (schema + fields)
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug} [get]
func (h *Handler) GetCollection(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	coll, err := h.uc.GetCollection(c.Request.Context(), ownerID, projectID, c.Param("slug"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toCollectionResponse(coll))
}

// UpdateCollection godoc
// @Summary  Update a CMS collection's label/description
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug} [patch]
func (h *Handler) UpdateCollection(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	var req UpdateCollectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	coll, err := h.uc.UpdateCollection(c.Request.Context(), ownerID, projectID, c.Param("slug"), UpdateCollectionInput{
		Name:         req.Name,
		Description:  req.Description,
		PrimaryField: req.PrimaryField,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toCollectionResponse(coll))
}

// DeleteCollection godoc
// @Summary  Delete a CMS collection and its Mongo documents
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug} [delete]
func (h *Handler) DeleteCollection(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	if err := h.uc.DeleteCollection(c.Request.Context(), ownerID, projectID, c.Param("slug")); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// ── Field ────────────────────────────────────────────────────────────────

// AddField godoc
// @Summary  Add a field to a collection's schema
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/fields [post]
func (h *Handler) AddField(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	var req FieldCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	field, err := h.uc.AddField(c.Request.Context(), ownerID, projectID, c.Param("slug"), FieldCreate{
		Name:                   req.Name,
		Label:                  req.Label,
		Type:                   req.Type,
		Required:               req.Required,
		Default:                req.Default,
		Validators:             req.Validators,
		RelationCollectionSlug: req.RelationCollection,
		Cascade:                req.Cascade,
		Index:                  req.Index,
		Order:                  req.Order,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toFieldResponse(field))
}

// UpdateField godoc
// @Summary  Update a field's definition
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/fields/{field_name} [patch]
func (h *Handler) UpdateField(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	var req FieldUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	field, err := h.uc.UpdateField(c.Request.Context(), ownerID, projectID, c.Param("slug"), c.Param("field_name"), FieldPatch{
		Label:                  req.Label,
		Required:               req.Required,
		Default:                req.Default,
		Validators:             req.Validators,
		RelationCollectionSlug: req.RelationCollection,
		ClearRelation:          req.ClearRelation,
		Cascade:                req.Cascade,
		Index:                  req.Index,
		Order:                  req.Order,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toFieldResponse(field))
}

// RemoveField godoc
// @Summary  Remove a field from a collection's schema
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/fields/{field_name} [delete]
func (h *Handler) RemoveField(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	if err := h.uc.RemoveField(c.Request.Context(), ownerID, projectID, c.Param("slug"), c.Param("field_name")); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// ── Document ─────────────────────────────────────────────────────────────

// InsertDocument godoc
// @Summary  Insert a document, validated against the collection's fields
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/documents [post]
func (h *Handler) InsertDocument(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	var doc map[string]any
	if err := c.ShouldBindJSON(&doc); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if bad := findForbiddenOperator(doc, 0); bad != "" {
		httpx.Error(c, httpx.BadRequest("cms.forbidden_operator", "operator "+bad+" is not allowed"))
		return
	}
	res, err := h.uc.InsertDocument(c.Request.Context(), ownerID, projectID, c.Param("slug"), doc)
	if err != nil {
		writeUcErr(c, err)
		return
	}
	httpx.OK(c, 201, res)
}

// FindDocuments godoc
// @Summary  List/find documents (filter, sort, pagination)
// @Tags     cms
// @Security BearerAuth
// @Param    filter  query  string  false  "JSON filter object"
// @Param    sort    query  string  false  "comma-separated fields, '-' prefix for desc"
// @Param    limit   query  int     false  "page size (default 50, max 500)"
// @Param    skip    query  int     false  "rows to skip"
// @Router   /cms/projects/{project_id}/collections/{slug}/documents [get]
func (h *Handler) FindDocuments(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}

	filter, ok := parseFilterQuery(c)
	if !ok {
		return
	}

	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 64)
	skip, _ := strconv.ParseInt(c.Query("skip"), 10, 64)

	var sort []DocumentSort
	if raw := c.Query("sort"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.HasPrefix(part, "-") {
				sort = append(sort, DocumentSort{Field: part[1:], Asc: false})
			} else {
				sort = append(sort, DocumentSort{Field: part, Asc: true})
			}
		}
	}

	list, err := h.uc.FindDocuments(c.Request.Context(), ownerID, projectID, c.Param("slug"), DocumentQuery{
		Filter:         filter,
		Sort:           sort,
		Limit:          limit,
		Skip:           skip,
		IncludeDeleted: c.Query("include_deleted") == "1",
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, DocumentListResponse{Results: list.Items, Total: list.Total, Limit: limit, Skip: skip})
}

// CountDocuments godoc
// @Summary  Count documents matching a filter
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/documents/count [get]
func (h *Handler) CountDocuments(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	filter, ok := parseFilterQuery(c)
	if !ok {
		return
	}
	n, err := h.uc.CountDocuments(c.Request.Context(), ownerID, projectID, c.Param("slug"), filter)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, CountResponse{Count: n})
}

// FindOneDocument godoc
// @Summary  Get a single document by id
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/documents/{doc_id} [get]
func (h *Handler) FindOneDocument(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	doc, err := h.uc.FindOneDocument(c.Request.Context(), ownerID, projectID, c.Param("slug"), c.Param("doc_id"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, doc)
}

// UpdateDocument godoc
// @Summary  Partially update a document, validated against the schema
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/documents/{doc_id} [patch]
func (h *Handler) UpdateDocument(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if bad := findForbiddenOperator(patch, 0); bad != "" {
		httpx.Error(c, httpx.BadRequest("cms.forbidden_operator", "operator "+bad+" is not allowed"))
		return
	}
	res, err := h.uc.UpdateDocument(c.Request.Context(), ownerID, projectID, c.Param("slug"), c.Param("doc_id"), patch)
	if err != nil {
		writeUcErr(c, err)
		return
	}
	httpx.OK(c, 200, res)
}

// DeleteDocument godoc
// @Summary  Soft-delete a document
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/documents/{doc_id} [delete]
func (h *Handler) DeleteDocument(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	if err := h.uc.DeleteDocument(c.Request.Context(), ownerID, projectID, c.Param("slug"), c.Param("doc_id")); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// RestoreDocument godoc
// @Summary  Undo a document soft-delete
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/documents/{doc_id}/restore [post]
func (h *Handler) RestoreDocument(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	if err := h.uc.RestoreDocument(c.Request.Context(), ownerID, projectID, c.Param("slug"), c.Param("doc_id")); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// BulkInsertDocuments godoc
// @Summary  Insert many documents in one call, each validated against the schema
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/documents/bulk [post]
func (h *Handler) BulkInsertDocuments(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	var docs []map[string]any
	if err := c.ShouldBindJSON(&docs); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body — expected a JSON array of documents"))
		return
	}
	for _, d := range docs {
		if bad := findForbiddenOperator(d, 0); bad != "" {
			httpx.Error(c, httpx.BadRequest("cms.forbidden_operator", "operator "+bad+" is not allowed"))
			return
		}
	}
	res, err := h.uc.InsertDocuments(c.Request.Context(), ownerID, projectID, c.Param("slug"), docs)
	if err != nil {
		writeUcErr(c, err)
		return
	}
	httpx.OK(c, 201, gin.H{"items": res})
}

// AggregateDocuments godoc
// @Summary  Run an aggregation pipeline scoped to this project's documents
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/documents/aggregate [post]
func (h *Handler) AggregateDocuments(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	var req struct {
		Pipeline []map[string]any `json:"pipeline"`
		Limit    int64            `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	for _, stage := range req.Pipeline {
		if bad := findForbiddenOperator(stage, 0); bad != "" {
			httpx.Error(c, httpx.BadRequest("cms.forbidden_operator", "operator "+bad+" is not allowed"))
			return
		}
	}
	res, err := h.uc.AggregateDocuments(c.Request.Context(), ownerID, projectID, c.Param("slug"), req.Pipeline, req.Limit)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, gin.H{"items": res})
}

func parseFilterQuery(c *gin.Context) (map[string]any, bool) {
	raw := c.Query("filter")
	if raw == "" {
		return nil, true
	}
	var filter map[string]any
	if err := json.Unmarshal([]byte(raw), &filter); err != nil {
		httpx.Error(c, httpx.BadRequest("cms.invalid_filter", "filter must be a JSON object"))
		return nil, false
	}
	if bad := findForbiddenOperator(filter, 0); bad != "" {
		httpx.Error(c, httpx.BadRequest("cms.forbidden_operator", "operator "+bad+" is not allowed in filter"))
		return nil, false
	}
	return filter, true
}

// ── Export task ──────────────────────────────────────────────────────────

// CreateExportTask godoc
// @Summary  Queue an Excel export of a collection's documents
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/export [post]
func (h *Handler) CreateExportTask(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathProjectID(c)
	if !ok {
		return
	}
	task, err := h.uc.CreateExportTask(c.Request.Context(), ownerID, projectID, c.Param("slug"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toExportTaskResponse(task))
}

// GetExportTaskStatus godoc
// @Summary  Poll an export task's status/progress/download url
// @Tags     cms
// @Security BearerAuth
// @Router   /cms/projects/{project_id}/collections/{slug}/export/{task_id} [get]
func (h *Handler) GetExportTaskStatus(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	taskID, err := uuid.Parse(c.Param("task_id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("cms.invalid_task_id", "invalid export task id"))
		return
	}
	task, err := h.uc.GetExportTask(c.Request.Context(), ownerID, taskID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toExportTaskResponse(task))
}
