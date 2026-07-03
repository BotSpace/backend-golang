package plan

import (
	"crypto/subtle"
	"errors"
	"os"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// internalTokenHeader/internalTokenEnv authenticate the AI-runner-facing
// usage-report endpoint out of band from user JWTs, mirroring Django's
// X-Internal-Token / settings.INTERNAL_API_TOKEN check in
// SubscriptionViewSet.report_usage. Read directly from the environment here
// (rather than internal/shared/config, which this module must not edit).
const (
	internalTokenHeader = "X-Internal-Token"
	internalTokenEnv    = "INTERNAL_API_TOKEN"
)

type Handler struct {
	uc Usecase
}

func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// authUserID extracts and parses the authenticated user id, writing a 401 and
// returning ok=false when it is missing/invalid.
func authUserID(c *gin.Context) (uuid.UUID, bool) {
	uid, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	return uid, true
}

// ListPlans godoc
// @Summary      List active pricing plans
// @Tags         plans
// @Produce      json
// @Success      200  {array}   PlanResponse
// @Router       /plans [get]
func (h *Handler) ListPlans(c *gin.Context) {
	plans, err := h.uc.ListActivePlans(c.Request.Context())
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toPlanListResponse(plans))
}

// Current godoc
// @Summary      Get the current user's subscription
// @Tags         subscription
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  SubscriptionResponse
// @Failure      401  {object}  httpx.ErrorResponse
// @Router       /subscription [get]
func (h *Handler) Current(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	sub, p, err := h.uc.Current(c.Request.Context(), uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toSubscriptionResponse(sub, p))
}

// Upgrade godoc
// @Summary      Upgrade the current user's subscription
// @Tags         subscription
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      UpgradeRequest  true  "New plan + billing period"
// @Success      200   {object}  UpgradeResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Failure      409   {object}  httpx.ErrorResponse
// @Router       /subscription/upgrade [post]
func (h *Handler) Upgrade(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	var req UpgradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if req.Months == 0 {
		req.Months = 1
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	planID, err := uuid.Parse(req.PlanID)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("plan.invalid_id", "invalid plan id"))
		return
	}
	sub, newPlan, previousPlanName, err := h.uc.Upgrade(c.Request.Context(), uid, planID, req.Months)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toUpgradeResponse(sub, newPlan, previousPlanName))
}

// Usage godoc
// @Summary      Get the current user's token/bot usage
// @Tags         subscription
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  UsageResponse
// @Router       /subscription/usage [get]
func (h *Handler) Usage(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	sub, p, err := h.uc.Usage(c.Request.Context(), uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toUsageResponse(sub, p))
}

// ReportUsage godoc
// @Summary      Report AI token usage for a user (internal, agentv3/worker)
// @Tags         subscription
// @Accept       json
// @Produce      json
// @Param        X-Internal-Token  header    string               true  "Shared internal token"
// @Param        body              body      ReportUsageRequest   true  "Usage report"
// @Success      200  {object}  ReportUsageResponse
// @Failure      403  {object}  httpx.ErrorResponse
// @Router       /subscription/usage/report [post]
func (h *Handler) ReportUsage(c *gin.Context) {
	expected := os.Getenv(internalTokenEnv)
	if expected == "" {
		httpx.Error(c, httpx.Internal(errors.New("server konfiguratsiya xatosi: INTERNAL_API_TOKEN o'rnatilmagan")))
		return
	}
	provided := c.GetHeader(internalTokenHeader)
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		httpx.Error(c, httpx.Forbidden("auth.forbidden", "ruxsat etilmagan so'rov"))
		return
	}

	var req ReportUsageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("user.invalid_id", "invalid user id"))
		return
	}
	var projectID *uuid.UUID
	if req.ProjectID != nil {
		pid, err := uuid.Parse(*req.ProjectID)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("project.invalid_id", "invalid project id"))
			return
		}
		projectID = &pid
	}

	billableInput, err := h.uc.ReportUsage(c.Request.Context(), ReportUsageInput{
		UserID:          userID,
		ProjectID:       projectID,
		InputTokens:     req.InputTokens,
		OutputTokens:    req.OutputTokens,
		CacheReadTokens: req.CacheReadTokens,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, ReportUsageResponse{
		UserID:            req.UserID,
		ProjectID:         req.ProjectID,
		InputTokensAdded:  billableInput,
		OutputTokensAdded: req.OutputTokens,
	})
}

// ===================== admin CRUD (Tarif boshqaruvi) =====================

// AdminList godoc
// @Summary      List every plan, active and inactive (admin)
// @Tags         plans-admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   PlanResponse
// @Router       /admin/plans [get]
func (h *Handler) AdminList(c *gin.Context) {
	plans, err := h.uc.AdminListPlans(c.Request.Context())
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toPlanListResponse(plans))
}

// AdminCreate godoc
// @Summary      Create a pricing plan (admin)
// @Tags         plans-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreatePlanRequest  true  "Plan payload"
// @Success      201   {object}  PlanResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /admin/plans [post]
func (h *Handler) AdminCreate(c *gin.Context) {
	var req CreatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	p, err := h.uc.AdminCreatePlan(c.Request.Context(), CreatePlanInput{
		Name:            req.Name,
		Price:           req.Price,
		Description:     req.Description,
		IsTop:           req.IsTop,
		IsActive:        req.IsActive,
		SortOrder:       req.SortOrder,
		MaxBots:         req.MaxBots,
		MaxTokens:       req.MaxTokens,
		CachedTokenRate: req.CachedTokenRate,
		Features:        req.Features,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toPlanResponse(p))
}

// AdminUpdate godoc
// @Summary      Update a pricing plan (admin)
// @Tags         plans-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string             true  "Plan id"
// @Param        body  body      UpdatePlanRequest  true  "Fields to update"
// @Success      200   {object}  PlanResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /admin/plans/{id} [patch]
func (h *Handler) AdminUpdate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("plan.invalid_id", "invalid plan id"))
		return
	}
	var req UpdatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	p, err := h.uc.AdminUpdatePlan(c.Request.Context(), id, UpdatePlanInput{
		Name:            req.Name,
		Price:           req.Price,
		Description:     req.Description,
		IsTop:           req.IsTop,
		IsActive:        req.IsActive,
		SortOrder:       req.SortOrder,
		MaxBots:         req.MaxBots,
		MaxTokens:       req.MaxTokens,
		CachedTokenRate: req.CachedTokenRate,
		Features:        req.Features,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toPlanResponse(p))
}

// AdminDelete godoc
// @Summary      Delete a pricing plan (admin)
// @Tags         plans-admin
// @Security     BearerAuth
// @Param        id   path      string  true  "Plan id"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Failure      409  {object}  httpx.ErrorResponse
// @Router       /admin/plans/{id} [delete]
func (h *Handler) AdminDelete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("plan.invalid_id", "invalid plan id"))
		return
	}
	if err := h.uc.AdminDeletePlan(c.Request.Context(), id); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}
