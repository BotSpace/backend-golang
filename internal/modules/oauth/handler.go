package oauth

import (
	"errors"
	"net/http"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc *Usecase
}

func NewHandler(uc *Usecase) *Handler {
	return &Handler{uc: uc}
}

// Metadata serves the RFC 8414 authorization-server discovery document.
func (h *Handler) Metadata(c *gin.Context) {
	c.JSON(http.StatusOK, h.uc.Metadata())
}

// Register is the RFC 7591 dynamic client registration endpoint (public).
func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeOAuthError(c, oauthErr("invalid_client_metadata", "invalid registration body"))
		return
	}
	resp, oerr := h.uc.Register(c.Request.Context(), req)
	if oerr != nil {
		writeOAuthError(c, oerr)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// AuthorizeMeta validates the OAuth request for the consent screen (public —
// the dashboard calls it before rendering, AllowAny like the Django original).
func (h *Handler) AuthorizeMeta(c *gin.Context) {
	clientID := c.Query("client_id")
	redirectURI := c.Query("redirect_uri")
	name, redirect, err := h.uc.AuthorizeMeta(c.Request.Context(), clientID, redirectURI)
	if err != nil {
		if errors.Is(err, ErrUnknownClient) {
			httpx.Error(c, httpx.BadRequest("oauth.unknown_client", "Unknown client_id"))
			return
		}
		if errors.Is(err, ErrBadRedirect) {
			httpx.Error(c, httpx.BadRequest("oauth.bad_redirect", "redirect_uri not registered for this client"))
			return
		}
		httpx.Error(c, httpx.Internal(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"client_name": name, "redirect_uri": redirect})
}

type approveBody struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	State               string `json:"state"`
	Scope               string `json:"scope"`
	Resource            string `json:"resource"`
}

// Approve mints an authorization code for the authenticated, consenting user.
// Requires a valid Bearer JWT (middleware.Auth) — the user_id is a UUID, which
// is exactly what the legacy Django server choked on.
func (h *Handler) Approve(c *gin.Context) {
	userID, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("oauth.unauthenticated", "authentication required"))
		return
	}
	var req approveBody
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("oauth.invalid_request", "invalid request body"))
		return
	}
	redirectURL, err := h.uc.Approve(c.Request.Context(), userID, ApproveParams{
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		ResponseType:        req.ResponseType,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		State:               req.State,
		Scope:               req.Scope,
		Resource:            req.Resource,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrUnknownClient):
			httpx.Error(c, httpx.BadRequest("oauth.unknown_client", "Unknown client_id"))
		case errors.Is(err, ErrBadRedirect):
			httpx.Error(c, httpx.BadRequest("oauth.bad_redirect", "redirect_uri not registered for this client"))
		default:
			httpx.Error(c, httpx.BadRequest("oauth.invalid_request", err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"redirect_url": redirectURL})
}

// Token is the OAuth token endpoint (public; PKCE is the proof). It accepts
// form-encoded params and returns spec-shaped JSON — {access_token,...} on
// success, {error, error_description} on failure.
func (h *Handler) Token(c *gin.Context) {
	req := TokenRequest{
		GrantType:    c.PostForm("grant_type"),
		Code:         c.PostForm("code"),
		RedirectURI:  c.PostForm("redirect_uri"),
		ClientID:     c.PostForm("client_id"),
		CodeVerifier: c.PostForm("code_verifier"),
		RefreshToken: c.PostForm("refresh_token"),
	}
	res, oerr := h.uc.Token(c.Request.Context(), req)
	if oerr != nil {
		writeOAuthError(c, oerr)
		return
	}
	c.JSON(http.StatusOK, res)
}

// writeOAuthError renders the RFC 6749 {error, error_description} body MCP
// clients parse. server_error maps to 500, everything else to 400.
func writeOAuthError(c *gin.Context, e *OAuthError) {
	status := http.StatusBadRequest
	if e.Code == "server_error" {
		status = http.StatusInternalServerError
	}
	c.JSON(status, gin.H{"error": e.Code, "error_description": e.Description})
}
