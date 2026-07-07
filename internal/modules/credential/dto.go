package credential

import "time"

// CredentialResponse is the owner-facing projection — data is always masked,
// mirroring Django's CredentialListSerializer.get_data. Full plaintext is
// never returned here (see the internal-only responses below).
type CredentialResponse struct {
	ID        string         `json:"id"`
	OwnerID   string         `json:"owner_id"`
	Name      string         `json:"name"`
	TypeKey   string         `json:"type_key"`
	Label     string         `json:"label"`
	Mode      string         `json:"mode"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func toResponse(d *Detail) CredentialResponse {
	typ, found := GetType(d.TypeKey)
	label, mode := d.TypeKey, ""
	if found {
		label, mode = typ.Label, typ.Mode
	}
	return CredentialResponse{
		ID:        d.ID.String(),
		OwnerID:   d.OwnerID.String(),
		Name:      d.Name,
		TypeKey:   d.TypeKey,
		Label:     label,
		Mode:      mode,
		Data:      maskData(typ, found, d.Data),
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}

// ListResponse is a paginated envelope for a credential listing.
type ListResponse struct {
	Items  []CredentialResponse `json:"items"`
	Total  int64                `json:"total"`
	Limit  int32                `json:"limit"`
	Offset int32                `json:"offset"`
}

func toListResponse(items []Detail, total int64, limit, offset int32) ListResponse {
	out := make([]CredentialResponse, len(items))
	for i := range items {
		out[i] = toResponse(&items[i])
	}
	return ListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// InternalResponse is the engine-facing projection — data is fully decrypted.
// Only served behind the X-Internal-Token check (see routes.go).
type InternalResponse struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	TypeKey string         `json:"type_key"`
	Mode    string         `json:"mode"`
	Data    map[string]any `json:"data"`
}

func toInternalResponse(d *Detail) InternalResponse {
	typ, found := GetType(d.TypeKey)
	mode := ""
	if found {
		mode = typ.Mode
	}
	return InternalResponse{
		ID:      d.ID.String(),
		Name:    d.Name,
		TypeKey: d.TypeKey,
		Mode:    mode,
		Data:    d.Data,
	}
}

// OAuthStartRequest is the POST /credentials/oauth/:provider/start body.
type OAuthStartRequest struct {
	TypeKey string `json:"type_key" validate:"required,max=40"`
}

// OAuthStartResponse mirrors OAuthStartView.post's raw (unenveloped)
// {"authorize_url": "..."} — agentv3's oauth_start() calls _unwrap_item,
// which accepts a flat object with no "data" wrapper.
type OAuthStartResponse struct {
	AuthorizeURL string `json:"authorize_url"`
}

type OptionsRequest struct {
	CredentialID string            `json:"credential_id" validate:"required,uuid"`
	Params       map[string]string `json:"params"`
}

type OptionsResponse struct {
	Options []OptionItem `json:"options"`
}

// CreateRequest is the POST /credentials body.
type CreateRequest struct {
	TypeKey string         `json:"type_key" validate:"required,max=40"`
	Name    string         `json:"name" validate:"required,min=1,max=120"`
	Data    map[string]any `json:"data" validate:"required"`
}

// UpdateRequest is the PATCH /credentials/:id body. Data is a pointer-ish
// concept expressed as a plain map: an absent "data" key in the JSON body
// binds to a nil map (unchanged); an explicit "data": {...} binds to a
// non-nil map (merged over the existing value).
type UpdateRequest struct {
	Name *string        `json:"name" validate:"omitempty,min=1,max=120"`
	Data map[string]any `json:"data"`
}

// maskData replaces secret field values with "••••<last4>", mirroring
// Django's _mask_data. oauth2 types have no field schema — access_token and
// refresh_token are masked by convention, everything else passes through.
func maskData(typ TypeDef, found bool, data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	if found && typ.Mode == ModeOAuth2 {
		secret := map[string]bool{"access_token": true, "refresh_token": true}
		for k, v := range data {
			if secret[k] {
				out[k] = maskValue(v)
			} else {
				out[k] = v
			}
		}
		return out
	}
	secretNames := map[string]bool{}
	if found {
		for _, f := range typ.Fields {
			if f.Secret {
				secretNames[f.Name] = true
			}
		}
	}
	for k, v := range data {
		if secretNames[k] {
			out[k] = maskValue(v)
		} else {
			out[k] = v
		}
	}
	return out
}

// maskValue returns "••••<last4>" for strings longer than 4 chars, else nil
// (matches Django's _mask helper, which hides short/empty secrets entirely).
func maskValue(v any) any {
	s, ok := v.(string)
	if !ok || len(s) <= 4 {
		return nil
	}
	return "••••" + s[len(s)-4:]
}
