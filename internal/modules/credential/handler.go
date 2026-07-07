package credential

import (
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc       Usecase
	panelURL string
}

// NewHandler wires the credential handler. panelURL is the dashboard/admin
// panel origin OAuthCallback's HTML response links/postMessages back to
// (config.Config.Google.OAuthPanelURL); empty falls back to
// defaultOAuthPanelURL.
func NewHandler(uc Usecase, panelURL string) *Handler {
	if panelURL == "" {
		panelURL = defaultOAuthPanelURL
	}
	return &Handler{uc: uc, panelURL: panelURL}
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

// pathID parses the :id path param, writing a 400 on failure.
func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("credential.invalid_id", "invalid credential id"))
		return uuid.Nil, false
	}
	return id, true
}

// List godoc
// @Summary      List my credentials
// @Tags         credentials
// @Produce      json
// @Security     BearerAuth
// @Param        type_key  query     string  false  "Filter by credential type"
// @Param        limit     query     int     false  "Page size (default 20, max 100)"
// @Param        offset    query     int     false  "Rows to skip"
// @Success      200  {object}  ListResponse
// @Router       /credentials [get]
func (h *Handler) List(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, total, err := h.uc.List(c.Request.Context(), ListFilter{
		OwnerID: ownerID,
		TypeKey: c.Query("type_key"),
		Limit:   int32(limit),
		Offset:  int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListResponse(items, total, int32(limit), int32(offset)))
}

// Types godoc
// @Summary      List credential types
// @Tags         credentials
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}  TypeDef
// @Router       /credentials/types [get]
func (h *Handler) Types(c *gin.Context) {
	if _, ok := owner(c); !ok {
		return
	}
	httpx.OK(c, 200, h.uc.ListTypes(c.Request.Context()))
}

// AdminTypes godoc
// @Summary      List credential types (admin, "Credential turlari" page)
// @Tags         credentials-admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}  TypeDef
// @Router       /admin/credential-types [get]
func (h *Handler) AdminTypes(c *gin.Context) {
	httpx.OK(c, 200, h.uc.ListTypes(c.Request.Context()))
}

// Create godoc
// @Summary      Create a credential
// @Tags         credentials
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateRequest  true  "Credential payload"
// @Success      201   {object}  CredentialResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /credentials [post]
func (h *Handler) Create(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	d, err := h.uc.Create(c.Request.Context(), CreateData{
		OwnerID: ownerID,
		TypeKey: req.TypeKey,
		Name:    req.Name,
		Data:    req.Data,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(d))
}

// Get godoc
// @Summary      Get a credential (masked data)
// @Tags         credentials
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Credential id"
// @Success      200  {object}  CredentialResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /credentials/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	d, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(d))
}

// Update godoc
// @Summary      Update a credential
// @Tags         credentials
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "Credential id"
// @Param        body  body      UpdateRequest  true  "Fields to update"
// @Success      200   {object}  CredentialResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /credentials/{id} [patch]
func (h *Handler) Update(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	d, err := h.uc.Update(c.Request.Context(), id, ownerID, UpdateData{
		Name: req.Name,
		Data: req.Data,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(d))
}

// Delete godoc
// @Summary      Delete a credential (soft delete)
// @Tags         credentials
// @Security     BearerAuth
// @Param        id   path      string  true  "Credential id"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /credentials/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), id, ownerID); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// OAuthStart godoc
// @Summary      Begin an OAuth2 consent flow for an oauth2-mode credential type
// @Tags         credentials
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        provider  path      string             true  "OAuth provider (e.g. google)"
// @Param        body      body      OAuthStartRequest  true  "Target credential type_key"
// @Success      200   {object}  OAuthStartResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /credentials/oauth/{provider}/start [post]
func (h *Handler) OAuthStart(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req OAuthStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	authorizeURL, err := h.uc.OAuthStart(c.Request.Context(), ownerID, c.Param("provider"), req.TypeKey)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, OAuthStartResponse{AuthorizeURL: authorizeURL})
}

// Options godoc
// @Summary      Load dynamic credential-backed field options
// @Tags         credentials
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        resource  path      string          true  "Resource name (google_spreadsheets/google_sheet_tabs/google_sheet_columns)"
// @Param        body      body      OptionsRequest  true  "Credential and dependency params"
// @Success      200       {object}  OptionsResponse
// @Failure      400       {object}  httpx.ErrorResponse
// @Failure      404       {object}  httpx.ErrorResponse
// @Router       /credentials/options/{resource} [post]
func (h *Handler) Options(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req OptionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	credentialID, err := uuid.Parse(req.CredentialID)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("credential.invalid_id", "invalid credential id"))
		return
	}
	options, err := h.uc.Options(c.Request.Context(), OptionsInput{
		OwnerID:      ownerID,
		Resource:     c.Param("resource"),
		CredentialID: credentialID,
		Params:       req.Params,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, OptionsResponse{Options: options})
}

// OAuthCallback godoc
// @Summary      Complete an OAuth2 consent flow (Google redirects here)
// @Tags         credentials
// @Produce      html
// @Param        provider  path      string  true  "OAuth provider (e.g. google)"
// @Param        code      query     string  false "Authorization code"
// @Param        state     query     string  false "Signed state token minted by OAuthStart"
// @Param        error     query     string  false "Provider-reported error (e.g. access_denied)"
// @Success      200  {string}  string  "tiny HTML page that postMessages the opener and closes"
// @Failure      400  {string}  string  "tiny HTML error page"
// @Router       /credentials/oauth/{provider}/callback [get]
//
// No Authorization header is present — this is the user's browser being
// redirected by Google, not an API call from the dashboard. The caller is
// authenticated via the signed `state` query param instead (verified inside
// h.uc.OAuthCallback); see routes.go for why this route is mounted outside
// the JWT-guarded group.
func (h *Handler) OAuthCallback(c *gin.Context) {
	provider := c.Param("provider")
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	err := h.uc.OAuthCallback(c.Request.Context(), provider, code, state, errorParam)
	if err != nil {
		_ = c.Error(err)
		writeOAuthErrorHTML(c, provider, h.panelURL, oauthErrorMessage(err))
		return
	}
	writeOAuthSuccessHTML(c, provider, h.panelURL)
}

// oauthErrorMessage extracts a safe-to-display message from err: the
// AppError's Message for 4xx (already a curated, non-sensitive string — see
// OAuthCallback in usecase.go), or a generic message for 5xx so an internal
// error's wrapped details never reach the browser.
func oauthErrorMessage(err error) string {
	ae, ok := httpx.AsAppError(err)
	if !ok || ae.Status >= 500 {
		return "something went wrong, please try again"
	}
	return ae.Message
}

// InternalByID godoc
// @Summary      Resolve a credential by id (decrypted, engine-only)
// @Tags         credentials-internal
// @Produce      json
// @Param        X-Internal-Token  header    string  true  "Internal service token"
// @Param        X-Project-Id      header    string  false "Project id — cross-tenant owner guard"
// @Param        id                path      string  true  "Credential id"
// @Success      200  {object}  InternalResponse
// @Failure      403  {object}  httpx.ErrorResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /internal/credentials/{id} [get]
func (h *Handler) InternalByID(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	projectID, ok := optionalProjectID(c)
	if !ok {
		return
	}
	d, err := h.uc.InternalGetByID(c.Request.Context(), id, projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toInternalResponse(d))
}

// InternalByName godoc
// @Summary      Resolve a credential by name (decrypted, engine-only)
// @Tags         credentials-internal
// @Produce      json
// @Param        X-Internal-Token  header    string  true  "Internal service token"
// @Param        X-Project-Id      header    string  true  "Project id — the owner is derived from it"
// @Param        name              query     string  true  "Credential name"
// @Success      200  {object}  InternalResponse
// @Failure      400  {object}  httpx.ErrorResponse
// @Failure      403  {object}  httpx.ErrorResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /internal/credentials/by-name [get]
func (h *Handler) InternalByName(c *gin.Context) {
	name := c.Query("name")
	if name == "" {
		httpx.Error(c, httpx.BadRequest("credential.name_required", "name query param required"))
		return
	}
	projectID, err := uuid.Parse(c.GetHeader("X-Project-Id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("credential.project_id_required", "X-Project-Id header required"))
		return
	}
	d, err := h.uc.InternalGetByName(c.Request.Context(), name, projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toInternalResponse(d))
}

// optionalProjectID parses the X-Project-Id header when present; an absent
// header is fine (nil, true), a malformed one is a 400 (nil, false).
func optionalProjectID(c *gin.Context) (*uuid.UUID, bool) {
	raw := c.GetHeader("X-Project-Id")
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("credential.invalid_project_id", "invalid X-Project-Id header"))
		return nil, false
	}
	return &id, true
}
