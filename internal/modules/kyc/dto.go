package kyc

import "time"

// VerificationResponse is the self-service projection (own submission).
type VerificationResponse struct {
	ID                 string     `json:"id"`
	FullName           string     `json:"full_name"`
	PassportImage      string     `json:"passport_image"`
	SelfieImage        string     `json:"selfie_image"`
	ResidenceReference string     `json:"residence_reference"`
	Status             string     `json:"status"`
	RejectionReason    string     `json:"rejection_reason,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ReviewedAt         *time.Time `json:"reviewed_at,omitempty"`
}

func toResponse(v *Verification) VerificationResponse {
	return VerificationResponse{
		ID:                 v.ID.String(),
		FullName:           v.FullName,
		PassportImage:      v.PassportImage,
		SelfieImage:        v.SelfieImage,
		ResidenceReference: v.ResidenceReference,
		Status:             v.Status,
		RejectionReason:    v.RejectionReason,
		CreatedAt:          v.CreatedAt,
		UpdatedAt:          v.UpdatedAt,
		ReviewedAt:         v.ReviewedAt,
	}
}

// StatusResponse mirrors the legacy GET /kyc/status/ payload.
type StatusResponse struct {
	IsVerified   bool                  `json:"is_verified"`
	Verification *VerificationResponse `json:"verification"`
}

func toStatusResponse(isVerified bool, latest *Verification) StatusResponse {
	resp := StatusResponse{IsVerified: isVerified}
	if latest != nil {
		vr := toResponse(latest)
		resp.Verification = &vr
	}
	return resp
}

// AdminVerificationResponse is the admin-facing projection, including the
// reviewer and the submitter's phone (joined from users).
type AdminVerificationResponse struct {
	ID                 string     `json:"id"`
	UserID             string     `json:"user_id"`
	UserPhone          string     `json:"user_phone"`
	FullName           string     `json:"full_name"`
	PassportImage      string     `json:"passport_image"`
	SelfieImage        string     `json:"selfie_image"`
	ResidenceReference string     `json:"residence_reference"`
	Status             string     `json:"status"`
	RejectionReason    string     `json:"rejection_reason,omitempty"`
	ReviewedBy         string     `json:"reviewed_by,omitempty"`
	ReviewedByPhone    string     `json:"reviewed_by_phone,omitempty"`
	ReviewedAt         *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func toAdminResponse(v *Verification) AdminVerificationResponse {
	resp := AdminVerificationResponse{
		ID:                 v.ID.String(),
		UserID:             v.UserID.String(),
		UserPhone:          v.UserPhone,
		FullName:           v.FullName,
		PassportImage:      v.PassportImage,
		SelfieImage:        v.SelfieImage,
		ResidenceReference: v.ResidenceReference,
		Status:             v.Status,
		RejectionReason:    v.RejectionReason,
		ReviewedByPhone:    v.ReviewedByPhone,
		ReviewedAt:         v.ReviewedAt,
		CreatedAt:          v.CreatedAt,
		UpdatedAt:          v.UpdatedAt,
	}
	if v.ReviewedBy != nil {
		resp.ReviewedBy = v.ReviewedBy.String()
	}
	return resp
}

// AdminListResponse is a paginated envelope for the admin listing.
type AdminListResponse struct {
	Items  []AdminVerificationResponse `json:"items"`
	Total  int64                       `json:"total"`
	Limit  int32                       `json:"limit"`
	Offset int32                       `json:"offset"`
}

func toAdminListResponse(items []Verification, total int64, limit, offset int32) AdminListResponse {
	out := make([]AdminVerificationResponse, len(items))
	for i := range items {
		out[i] = toAdminResponse(&items[i])
	}
	return AdminListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// SubmitRequest is the multipart form payload for POST /kyc. Files are bound
// separately by the handler (gin has no struct tag for multipart files here);
// this type only documents/validates the text field.
type SubmitRequest struct {
	FullName string `form:"full_name" validate:"required,min=1,max=255"`
}

// RejectRequest is the admin rejection payload.
type RejectRequest struct {
	RejectionReason string `json:"rejection_reason" validate:"required,max=500"`
}

// ApproveRequest is the admin approval payload. Note is accepted for parity
// with the Django endpoint but is not persisted (no audit-log module here).
type ApproveRequest struct {
	Note string `json:"note" validate:"omitempty,max=255"`
}
