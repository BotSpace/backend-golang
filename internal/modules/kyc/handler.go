package kyc

import (
	"fmt"
	"os"
	"path/filepath"
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

// Size/extension limits mirror core/apps/shared/validators.py
// (validate_image_size, validate_document_size, IMAGE_EXTENSIONS, DOCUMENT_EXTENSIONS).
const (
	maxImageBytes    int64 = 10 << 20 // 10MB
	maxDocumentBytes int64 = 25 << 20 // 25MB
)

var (
	imageExtensions = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true}
	// residence_reference accepts an image or a document (Django's reference_extension_validator).
	referenceExtensions = map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
		".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".txt": true,
	}
)

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

// pathID parses the :id path param, writing a 400 on failure.
func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("kyc.invalid_id", "invalid verification id"))
		return uuid.Nil, false
	}
	return id, true
}

// mediaDir is the root uploads are saved under, configurable so deployments
// can share a volume with the Django MEDIA_ROOT. Defaults to ./media.
func mediaDir() string {
	if dir := os.Getenv("APP_MEDIA_DIR"); dir != "" {
		return dir
	}
	return "./media"
}

// saveUpload reads a multipart field, validates its extension and size, and
// saves it under mediaDir()/kyc/<subdir>/<uuid><ext>. It returns the path
// relative to mediaDir() — what gets stored in the DB — mirroring Django's
// FileField.name (a MEDIA_ROOT-relative path).
func saveUpload(c *gin.Context, field, subdir string, allowed map[string]bool, maxBytes int64) (string, error) {
	fh, err := c.FormFile(field)
	if err != nil {
		return "", fmt.Errorf("%s is required", field)
	}
	if fh.Size > maxBytes {
		return "", fmt.Errorf("%s exceeds the maximum allowed size", field)
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !allowed[ext] {
		return "", fmt.Errorf("%s has an unsupported file extension %q", field, ext)
	}

	relPath := filepath.Join("kyc", subdir, uuid.New().String()+ext)
	fullPath := filepath.Join(mediaDir(), relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("create upload dir: %w", err)
	}
	if err := c.SaveUploadedFile(fh, fullPath); err != nil {
		return "", fmt.Errorf("save upload: %w", err)
	}
	return relPath, nil
}

// Submit godoc
// @Summary      Submit a KYC verification request
// @Description  Multipart upload: full_name, passport_image, selfie_image, residence_reference.
// @Tags         kyc
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Success      201  {object}  VerificationResponse
// @Failure      400  {object}  httpx.ErrorResponse
// @Failure      409  {object}  httpx.ErrorResponse
// @Router       /kyc [post]
func (h *Handler) Submit(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}

	fullName := strings.TrimSpace(c.PostForm("full_name"))
	if details, err := validator.Struct(SubmitRequest{FullName: fullName}); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}

	passportPath, err := saveUpload(c, "passport_image", "passport", imageExtensions, maxImageBytes)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("kyc.invalid_passport_image", err.Error()))
		return
	}
	selfiePath, err := saveUpload(c, "selfie_image", "selfie", imageExtensions, maxImageBytes)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("kyc.invalid_selfie_image", err.Error()))
		return
	}
	residencePath, err := saveUpload(c, "residence_reference", "residence", referenceExtensions, maxDocumentBytes)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("kyc.invalid_residence_reference", err.Error()))
		return
	}

	v, err := h.uc.Submit(c.Request.Context(), SubmitInput{
		UserID:             uid,
		FullName:           fullName,
		PassportImage:      passportPath,
		SelfieImage:        selfiePath,
		ResidenceReference: residencePath,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(v))
}

// Status godoc
// @Summary      Get current user's KYC status
// @Tags         kyc
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  StatusResponse
// @Router       /kyc/status [get]
func (h *Handler) Status(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	verified, latest, err := h.uc.Status(c.Request.Context(), uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toStatusResponse(verified, latest))
}

// AdminList godoc
// @Summary      List KYC verification requests (admin)
// @Tags         kyc-admin
// @Produce      json
// @Security     BearerAuth
// @Param        status  query     string  false  "Filter by status (pending/approved/rejected)"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  AdminListResponse
// @Router       /kyc-verifications [get]
func (h *Handler) AdminList(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, total, err := h.uc.List(c.Request.Context(), ListFilter{
		Status: c.Query("status"),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminListResponse(items, total, int32(limit), int32(offset)))
}

// AdminGet godoc
// @Summary      Get a KYC verification request (admin)
// @Tags         kyc-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Verification id"
// @Success      200  {object}  AdminVerificationResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /kyc-verifications/{id} [get]
func (h *Handler) AdminGet(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	v, err := h.uc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminResponse(v))
}

// Approve godoc
// @Summary      Approve a KYC verification request (admin)
// @Description  Sets the submitter's kyc_verified_at, unlocking withdrawals.
// @Tags         kyc-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string          true  "Verification id"
// @Param        body  body      ApproveRequest  false  "Optional note"
// @Success      200  {object}  AdminVerificationResponse
// @Failure      409  {object}  httpx.ErrorResponse
// @Router       /kyc-verifications/{id}/approve [patch]
func (h *Handler) Approve(c *gin.Context) {
	reviewerID, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req ApproveRequest
	_ = c.ShouldBindJSON(&req) // note is optional; there is no audit-log module here to persist it

	v, err := h.uc.Approve(c.Request.Context(), id, reviewerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminResponse(v))
}

// Reject godoc
// @Summary      Reject a KYC verification request (admin)
// @Tags         kyc-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "Verification id"
// @Param        body  body      RejectRequest  true  "Rejection reason"
// @Success      200  {object}  AdminVerificationResponse
// @Failure      400  {object}  httpx.ErrorResponse
// @Failure      409  {object}  httpx.ErrorResponse
// @Router       /kyc-verifications/{id}/reject [patch]
func (h *Handler) Reject(c *gin.Context) {
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
	v, err := h.uc.Reject(c.Request.Context(), id, reviewerID, req.RejectionReason)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminResponse(v))
}
