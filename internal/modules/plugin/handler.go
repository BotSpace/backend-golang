package plugin

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// internalTokenEnv is the shared secret for engine<->backend + CI internal
// endpoints (X-Internal-Token), same var the debug/internal endpoints use.
const internalTokenEnv = "APP_INTERNAL_TOKEN"

// githubWebhookSecretEnv signs the GitHub App push webhook (X-Hub-Signature-256).
// TODO: move to config.Config once this module is wired centrally; for now it
// is read directly, matching how INTERNAL_API_TOKEN is consumed in Django.
const githubWebhookSecretEnv = "APP_GITHUB_WEBHOOK_SECRET"

type Handler struct {
	uc Usecase
}

func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// ---- shared helpers -----------------------------------------------------

func owner(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	return id, true
}

func projectID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("project.invalid_id", "invalid project id"))
		return uuid.Nil, false
	}
	return id, true
}

// requireInternalToken gates CI/engine-internal endpoints with a constant-time
// compare against APP_INTERNAL_TOKEN. Fails closed: an unset secret always
// rejects (mirrors the Django module-trigger view's "fail-closed" comment).
func requireInternalToken(c *gin.Context) bool {
	expected := os.Getenv(internalTokenEnv)
	got := c.GetHeader("X-Internal-Token")
	if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_internal_token", "invalid or missing X-Internal-Token"))
		return false
	}
	return true
}

// ---- Catalog --------------------------------------------------------------

// ListModules godoc
// @Summary  List the module catalog
// @Tags     modules
// @Security BearerAuth
// @Router   /modules [get]
func (h *Handler) ListModules(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	f := ModuleListFilter{Search: c.Query("search"), Limit: int32(limit), Offset: int32(offset)}
	if v := c.Query("active"); v == "true" || v == "false" {
		b := v == "true"
		f.Active = &b
	}
	items, total, err := h.uc.ListModules(c.Request.Context(), f)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, toModuleListResponse(items, total, f.Limit, f.Offset))
}

// GetModule godoc
// @Summary  Get a module by slug
// @Tags     modules
// @Security BearerAuth
// @Router   /modules/{slug} [get]
func (h *Handler) GetModule(c *gin.Context) {
	m, err := h.uc.GetModule(c.Request.Context(), c.Param("slug"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, toModuleResponse(m))
}

// CreateModule godoc
// @Summary  Register a module in the catalog (admin)
// @Tags     modules
// @Security BearerAuth
// @Router   /modules [post]
func (h *Handler) CreateModule(c *gin.Context) {
	var req CreateModuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	m, err := h.uc.CreateModule(c.Request.Context(), CreateModuleInput{
		Slug: req.Slug, Name: req.Name, Description: req.Description, Icon: req.Icon,
		GithubURL: req.GithubURL, Image: req.Image, Port: req.Port, AuthToken: req.AuthToken,
		Active: req.Active, IsDefault: req.IsDefault,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusCreated, toModuleResponse(m))
}

// UpdateModule godoc
// @Summary  Update a module (admin)
// @Tags     modules
// @Security BearerAuth
// @Router   /modules/{slug} [patch]
func (h *Handler) UpdateModule(c *gin.Context) {
	var req UpdateModuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	m, err := h.uc.UpdateModule(c.Request.Context(), c.Param("slug"), UpdateModuleInput{
		Name: req.Name, Description: req.Description, Icon: req.Icon, GithubURL: req.GithubURL,
		Image: req.Image, Port: req.Port, AuthToken: req.AuthToken, Active: req.Active, IsDefault: req.IsDefault,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, toModuleResponse(m))
}

// DeleteModule godoc
// @Summary  Remove a module from the catalog (admin)
// @Tags     modules
// @Security BearerAuth
// @Router   /modules/{slug} [delete]
func (h *Handler) DeleteModule(c *gin.Context) {
	if err := h.uc.DeleteModule(c.Request.Context(), c.Param("slug")); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// RefreshManifest godoc
// @Summary  Re-run describe()/docs() against the module endpoint (admin)
// @Tags     modules
// @Security BearerAuth
// @Router   /modules/{slug}/refresh-manifest [post]
func (h *Handler) RefreshManifest(c *gin.Context) {
	m, err := h.uc.RefreshManifest(c.Request.Context(), c.Param("slug"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, toModuleResponse(m))
}

// Redeploy godoc
// @Summary  CI redeploy hook — recreate the shared pod (X-Internal-Token)
// @Tags     modules
// @Router   /modules/{slug}/redeploy [post]
func (h *Handler) Redeploy(c *gin.Context) {
	if !requireInternalToken(c) {
		return
	}
	m, refreshed, err := h.uc.Redeploy(c.Request.Context(), c.Param("slug"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, RedeployResponse{OK: true, Module: m.Slug, ManifestRefreshed: refreshed})
}

// githubPushPayload is the subset of the GitHub push webhook payload we need.
type githubPushPayload struct {
	Ref   string `json:"ref"`
	After string `json:"after"`
	Repo  struct {
		HTMLURL       string `json:"html_url"`
		CloneURL      string `json:"clone_url"`
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
}

// GithubWebhook godoc
// @Summary  GitHub App push webhook — build & redeploy the matching module
// @Tags     modules
// @Router   /modules/github-webhook [post]
func (h *Handler) GithubWebhook(c *gin.Context) {
	secret := os.Getenv(githubWebhookSecretEnv)
	if secret == "" {
		httpx.Error(c, &httpx.AppError{Code: "module.webhook_not_configured", Message: "webhook not configured", Status: http.StatusServiceUnavailable})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "cannot read body"))
		return
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	signature := c.GetHeader("X-Hub-Signature-256")
	if subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) != 1 {
		httpx.Error(c, httpx.Forbidden("module.invalid_signature", "invalid signature"))
		return
	}

	event := c.GetHeader("X-GitHub-Event")
	if event == "ping" {
		httpx.OK(c, http.StatusOK, GithubWebhookResponse{OK: true, Pong: true})
		return
	}
	if event != "push" {
		httpx.OK(c, http.StatusOK, GithubWebhookResponse{OK: true, IgnoredEvent: event})
		return
	}

	var payload githubPushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid payload"))
		return
	}
	defaultBranch := payload.Repo.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	result, err := h.uc.HandleGithubPush(c.Request.Context(), payload.Repo.HTMLURL, payload.Repo.CloneURL,
		payload.Repo.FullName, payload.Ref, defaultBranch, payload.After)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, GithubWebhookResponse{
		OK: result.OK, Module: result.Module, SHA: result.SHA,
		IgnoredRef: result.IgnoredRef, NoModuleFor: result.NoModuleFor,
	})
}

// ---- Project scope ---------------------------------------------------------

// ListProjectModules godoc
// @Summary  Modules enabled for a project
// @Tags     modules
// @Security BearerAuth
// @Router   /projects/{id}/modules [get]
func (h *Handler) ListProjectModules(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	pID, ok := projectID(c)
	if !ok {
		return
	}
	items, err := h.uc.ListProjectModules(c.Request.Context(), pID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, toProjectModuleListResponse(items))
}

// EnableModule godoc
// @Summary  Enable a catalog module for a project
// @Tags     modules
// @Security BearerAuth
// @Router   /projects/{id}/modules [post]
func (h *Handler) EnableModule(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	pID, ok := projectID(c)
	if !ok {
		return
	}
	var req EnableModuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	pm, created, err := h.uc.EnableModule(c.Request.Context(), pID, ownerID, req.ModuleSlug)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.OK(c, status, toProjectModuleResponse(pm))
}

// DisableModule godoc
// @Summary  Disable a module for a project
// @Tags     modules
// @Security BearerAuth
// @Router   /projects/{id}/modules/{slug} [delete]
func (h *Handler) DisableModule(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	pID, ok := projectID(c)
	if !ok {
		return
	}
	if err := h.uc.DisableModule(c.Request.Context(), pID, ownerID, c.Param("slug")); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// ModuleManifests godoc
// @Summary  Flat node manifests of a project's enabled modules
// @Tags     modules
// @Security BearerAuth
// @Router   /projects/{id}/module-manifests [get]
func (h *Handler) ModuleManifests(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	pID, ok := projectID(c)
	if !ok {
		return
	}
	manifests, err := h.uc.ModuleManifests(c.Request.Context(), pID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, ManifestsResponse{Manifests: manifests})
}

// NodeManifests godoc
// @Summary  Unified node manifests (built-in + enabled modules)
// @Tags     modules
// @Security BearerAuth
// @Router   /projects/{id}/node-manifests [get]
func (h *Handler) NodeManifests(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	pID, ok := projectID(c)
	if !ok {
		return
	}
	manifests, err := h.uc.NodeManifests(c.Request.Context(), pID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, ManifestsResponse{Manifests: manifests})
}

// ModulesForEngine godoc
// @Summary  Engine-facing modules.json data for a project's enabled modules
// @Tags     modules
// @Security BearerAuth
// @Router   /projects/{id}/modules-for-engine [get]
func (h *Handler) ModulesForEngine(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	pID, ok := projectID(c)
	if !ok {
		return
	}
	items, err := h.uc.ModulesForEngine(c.Request.Context(), pID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, gin.H{"modules": toEngineModulesResponse(items)})
}

// ---- Internal relay + module-scoped file endpoints -------------------------

// ModuleTrigger godoc
// @Summary  Internal relay: modul -> engine event (X-Internal-Token)
// @Tags     modules
// @Router   /module-trigger/{project_uuid} [post]
func (h *Handler) ModuleTrigger(c *gin.Context) {
	if !requireInternalToken(c) {
		return
	}
	pUUID, err := uuid.Parse(c.Param("project_uuid"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("project.invalid_id", "invalid project uuid"))
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "cannot read body"))
		return
	}
	if err := h.uc.RelayTrigger(c.Request.Context(), pUUID, body); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, gin.H{"detail": "ok"})
}

// verifyModuleFileToken checks the HMAC-scoped bearer token module servers use
// to call back into the platform on behalf of a project:
// `Bearer <project_id>:<hex(HMAC-SHA256(APP_INTERNAL_TOKEN, project_id))>`
// (Django views.module_file._verify_module_token). Returns the project id on
// success.
func verifyModuleFileToken(c *gin.Context) (string, bool) {
	auth := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		httpx.Error(c, httpx.Unauthorized("auth.missing_token", "missing bearer token"))
		return "", false
	}
	token := strings.TrimPrefix(auth, prefix)
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token format"))
		return "", false
	}
	projID, providedHex := parts[0], parts[1]

	secret := os.Getenv(internalTokenEnv)
	if secret == "" {
		httpx.Error(c, httpx.Internal(errors.New("server misconfigured: "+internalTokenEnv+" is not set")))
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(projID))
	expectedHex := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(providedHex)), []byte(expectedHex)) != 1 {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return "", false
	}
	return projID, true
}

// UploadModuleFile godoc
// @Summary  Module-scoped file upload (HMAC bearer token)
// @Tags     module-file
// @Router   /module-file [post]
func (h *Handler) UploadModuleFile(c *gin.Context) {
	if _, ok := verifyModuleFileToken(c); !ok {
		return
	}
	// TODO: the `file` module (Django FileModel/ResourceModel) has not been
	// ported yet (porting guide migration 000012). Wire real storage here once
	// it lands; until then the token contract is enforced but uploads 501.
	httpx.Error(c, &httpx.AppError{
		Code: "module_file.not_implemented", Message: "module file storage not implemented yet",
		Status: http.StatusNotImplemented,
	})
}

// DeleteModuleFile godoc
// @Summary  Module-scoped file delete (HMAC bearer token)
// @Tags     module-file
// @Router   /module-file/{uuid} [delete]
func (h *Handler) DeleteModuleFile(c *gin.Context) {
	if _, ok := verifyModuleFileToken(c); !ok {
		return
	}
	// TODO: see UploadModuleFile — file module not ported yet.
	httpx.Error(c, &httpx.AppError{
		Code: "module_file.not_implemented", Message: "module file storage not implemented yet",
		Status: http.StatusNotImplemented,
	})
}
