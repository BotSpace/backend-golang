package template

import (
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateSubmission godoc
// @Summary      Submit a project as a template for review
// @Tags         template-submission
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateSubmissionRequest  true  "Submission payload"
// @Success      201   {object}  SubmissionResponse
// @Router       /template/submissions [post]
func (h *Handler) CreateSubmission(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	var req CreateSubmissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	projectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("template.invalid_project_id", "invalid project_id"))
		return
	}
	categoryIDs, err := parseUUIDs(req.CategoryIDs)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("template.invalid_category_id", err.Error()))
		return
	}
	featureIDs, err := parseUUIDs(req.FeatureIDs)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("template.invalid_feature_id", err.Error()))
		return
	}
	s, err := h.uc.CreateSubmission(c.Request.Context(), CreateSubmissionInput{
		UserID:           uid,
		ProjectID:        projectID,
		Name:             req.Name,
		ShortDescription: req.ShortDescription,
		Description:      req.Description,
		Image:            req.Image,
		Video:            req.Video,
		DemoURL:          req.DemoURL,
		OriginalPrice:    req.OriginalPrice,
		CategoryIDs:      categoryIDs,
		FeatureIDs:       featureIDs,
		RequiredEnvs:     req.RequiredEnvs,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toSubmissionResponse(s))
}

// ListSubmissions godoc
// @Summary      List my template submissions
// @Tags         template-submission
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query     int  false  "Page size (default 20, max 100)"
// @Param        offset  query     int  false  "Rows to skip"
// @Success      200  {object}  SubmissionListResponse
// @Router       /template/submissions [get]
func (h *Handler) ListSubmissions(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	items, total, err := h.uc.ListSubmissions(c.Request.Context(), uid, limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toSubmissionListResponse(items, total, limit, offset))
}

// GetSubmission godoc
// @Summary      Get one of my template submissions
// @Tags         template-submission
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Submission id"
// @Success      200  {object}  SubmissionResponse
// @Router       /template/submissions/{id} [get]
func (h *Handler) GetSubmission(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	s, err := h.uc.GetSubmission(c.Request.Context(), id, uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toSubmissionResponse(s))
}

// UpdateSubmission godoc
// @Summary      Update a pending template submission
// @Tags         template-submission
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string                   true  "Submission id"
// @Param        body  body      UpdateSubmissionRequest  true  "Fields to update"
// @Success      200   {object}  SubmissionResponse
// @Router       /template/submissions/{id} [patch]
func (h *Handler) UpdateSubmission(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req UpdateSubmissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	in := UpdateSubmissionInput{
		Name:             req.Name,
		ShortDescription: req.ShortDescription,
		Description:      req.Description,
		Image:            req.Image,
		Video:            req.Video,
		DemoURL:          req.DemoURL,
		OriginalPrice:    req.OriginalPrice,
		RequiredEnvs:     req.RequiredEnvs,
	}
	if req.CategoryIDs != nil {
		ids, err := parseUUIDs(req.CategoryIDs)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("template.invalid_category_id", err.Error()))
			return
		}
		in.CategoryIDs = ids
	}
	if req.FeatureIDs != nil {
		ids, err := parseUUIDs(req.FeatureIDs)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("template.invalid_feature_id", err.Error()))
			return
		}
		in.FeatureIDs = ids
	}
	s, err := h.uc.UpdateSubmission(c.Request.Context(), id, uid, in)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toSubmissionResponse(s))
}

// DeleteSubmission godoc
// @Summary      Delete a pending template submission
// @Tags         template-submission
// @Security     BearerAuth
// @Param        id   path  string  true  "Submission id"
// @Success      204
// @Router       /template/submissions/{id} [delete]
func (h *Handler) DeleteSubmission(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.uc.DeleteSubmission(c.Request.Context(), id, uid); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// ListSubmissionsAdmin godoc
// @Summary      List all template submissions (admin)
// @Tags         template-submission
// @Produce      json
// @Security     BearerAuth
// @Param        status  query     string  false  "Filter by status (pending|approved|rejected)"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  SubmissionListResponse
// @Router       /template/submissions/admin [get]
func (h *Handler) ListSubmissionsAdmin(c *gin.Context) {
	limit, offset := pageParams(c)
	items, total, err := h.uc.ListSubmissionsAdmin(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toSubmissionListResponse(items, total, limit, offset))
}

// ApproveSubmission godoc
// @Summary      Approve a submission and publish it as a template (admin)
// @Tags         template-submission
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string                    true  "Submission id"
// @Param        body  body      ApproveSubmissionRequest  false  "Optional launch discount"
// @Success      200   {object}  SubmissionResponse
// @Router       /template/submissions/{id}/approve [patch]
func (h *Handler) ApproveSubmission(c *gin.Context) {
	reviewerID, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req ApproveSubmissionRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
			return
		}
	}
	s, _, err := h.uc.ApproveSubmission(c.Request.Context(), id, reviewerID, req.Discount, req.DiscountType)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toSubmissionResponse(s))
}

// RejectSubmission godoc
// @Summary      Reject a pending submission (admin)
// @Tags         template-submission
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "Submission id"
// @Param        body  body      RejectRequest  true  "Rejection reason"
// @Success      200   {object}  SubmissionResponse
// @Router       /template/submissions/{id}/reject [patch]
func (h *Handler) RejectSubmission(c *gin.Context) {
	reviewerID, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req RejectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	s, err := h.uc.RejectSubmission(c.Request.Context(), id, reviewerID, req.RejectionReason)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toSubmissionResponse(s))
}
