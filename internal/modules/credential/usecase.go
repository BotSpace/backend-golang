package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/botspace/backend/internal/shared/crypto"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Detail pairs a Credential with its decrypted field data. The usecase always
// decrypts (data at rest is opaque to everyone else); masking for the
// owner-facing endpoints happens in dto.go, right before the JSON response is
// built, so the plaintext never leaks into a log line by accident.
type Detail struct {
	Credential
	Data map[string]any
}

// CreateData is the owner-facing create payload (plaintext fields — the
// usecase encrypts before it ever reaches the repository).
type CreateData struct {
	OwnerID uuid.UUID
	TypeKey string
	Name    string
	Data    map[string]any
}

// UpdateData carries optional fields; nil Data means "leave data unchanged".
// A non-nil (possibly empty) Data map is merged over the existing data —
// empty/omitted secret fields keep their old value (password-manager UX).
type UpdateData struct {
	Name *string
	Data map[string]any
}

// Usecase exposes owner-scoped credential operations plus the two internal
// (decrypted) lookups used by the engine.
type Usecase interface {
	Create(ctx context.Context, in CreateData) (*Detail, error)
	Get(ctx context.Context, id, ownerID uuid.UUID) (*Detail, error)
	Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateData) (*Detail, error)
	Delete(ctx context.Context, id, ownerID uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]Detail, int64, error)
	ListTypes(ctx context.Context) []TypeDef

	// InternalGetByID resolves a credential by id for the engine. projectID,
	// when non-nil, must own the credential (cross-tenant guard) — mirrors
	// core.apps.api.views.internal.credential_by_id.
	InternalGetByID(ctx context.Context, id uuid.UUID, projectID *uuid.UUID) (*Detail, error)
	// InternalGetByName resolves a credential by its owner-unique name, with
	// the owner derived from projectID — mirrors credential_by_name.
	InternalGetByName(ctx context.Context, name string, projectID uuid.UUID) (*Detail, error)

	// OAuthStart begins a browser OAuth2 consent flow for an oauth2-mode
	// credential type — mirrors OAuthStartView.post. Returns the Google
	// consent URL to redirect the user's browser to; the actual token
	// exchange + Credential row creation happens in Django's (unported)
	// OAuthCallbackView once Google redirects back — see oauth_state.go's
	// package doc for why the state token must stay Django-signing-compatible.
	OAuthStart(ctx context.Context, ownerID uuid.UUID, provider, typeKey string) (authorizeURL string, err error)
}

type usecase struct {
	repo   Repository
	cipher *crypto.Cipher
	oauth  OAuthConfig
}

// NewUsecase wires the credential usecase. cipher encrypts data at rest and
// decrypts it on every read (owner-masked or internal-raw). oauth carries the
// provider app credentials OAuthStart needs (see OAuthConfig doc comment).
func NewUsecase(repo Repository, cipher *crypto.Cipher, oauth OAuthConfig) Usecase {
	return &usecase{repo: repo, cipher: cipher, oauth: oauth}
}

func (u *usecase) Create(ctx context.Context, in CreateData) (*Detail, error) {
	typ, ok := GetType(in.TypeKey)
	if !ok {
		return nil, httpx.BadRequest("credential.unknown_type", fmt.Sprintf("unknown type_key %q", in.TypeKey))
	}
	data := in.Data
	if data == nil {
		data = map[string]any{}
	}
	if typ.Mode != ModeOAuth2 {
		if err := validateFields(typ.Fields, data); err != nil {
			return nil, err
		}
	}
	encrypted, err := u.encrypt(data)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	c, err := u.repo.Create(ctx, CreateInput{
		OwnerID:       in.OwnerID,
		TypeKey:       in.TypeKey,
		Name:          in.Name,
		DataEncrypted: encrypted,
	})
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return &Detail{Credential: *c, Data: data}, nil
}

func (u *usecase) Get(ctx context.Context, id, ownerID uuid.UUID) (*Detail, error) {
	c, err := u.repo.GetForOwner(ctx, id, ownerID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return u.decryptDetail(c)
}

func (u *usecase) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateData) (*Detail, error) {
	existing, err := u.repo.GetForOwner(ctx, id, ownerID)
	if err != nil {
		return nil, mapNotFound(err)
	}

	upd := UpdateInput{Name: in.Name}
	mergedData := map[string]any{}
	if in.Data != nil {
		oldData, err := u.decrypt(existing.DataEncrypted)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		typ, found := GetType(existing.TypeKey)

		merged := map[string]any{}
		for k, v := range oldData {
			merged[k] = v
		}
		if found && typ.Mode == ModeOAuth2 {
			for k, v := range in.Data {
				merged[k] = v
			}
		} else {
			secretNames := map[string]bool{}
			if found {
				for _, f := range typ.Fields {
					if f.Secret {
						secretNames[f.Name] = true
					}
				}
			}
			for k, v := range in.Data {
				if secretNames[k] && isEmptyValue(v) {
					continue // keep old secret value
				}
				merged[k] = v
			}
			if found && typ.Mode != ModeOAuth2 {
				if err := validateFields(typ.Fields, merged); err != nil {
					return nil, err
				}
			}
		}

		encrypted, err := u.encrypt(merged)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		upd.DataEncrypted = &encrypted
		mergedData = merged
	}

	c, err := u.repo.Update(ctx, id, ownerID, upd)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if in.Data == nil {
		return u.decryptDetail(c)
	}
	return &Detail{Credential: *c, Data: mergedData}, nil
}

func (u *usecase) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	if err := u.repo.SoftDelete(ctx, id, ownerID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("credential.not_found", "credential not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) List(ctx context.Context, f ListFilter) ([]Detail, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.List(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.Count(ctx, f.OwnerID, f.TypeKey)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	out := make([]Detail, len(items))
	for i := range items {
		d, err := u.decryptDetail(&items[i])
		if err != nil {
			return nil, 0, err
		}
		out[i] = *d
	}
	return out, total, nil
}

func (u *usecase) ListTypes(_ context.Context) []TypeDef {
	return ListTypes()
}

func (u *usecase) InternalGetByID(ctx context.Context, id uuid.UUID, projectID *uuid.UUID) (*Detail, error) {
	c, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if projectID != nil {
		ownerID, err := u.repo.GetProjectOwner(ctx, *projectID)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, httpx.Forbidden("credential.project_not_found", "project not found")
			}
			return nil, httpx.Internal(err)
		}
		if ownerID != c.OwnerID {
			return nil, httpx.Forbidden("credential.cross_tenant", "credential does not belong to this project's owner")
		}
	}
	return u.decryptDetail(c)
}

func (u *usecase) InternalGetByName(ctx context.Context, name string, projectID uuid.UUID) (*Detail, error) {
	ownerID, err := u.repo.GetProjectOwner(ctx, projectID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.Forbidden("credential.project_not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	c, err := u.repo.GetByOwnerAndName(ctx, ownerID, name)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return u.decryptDetail(c)
}

// oauthStateSecret is read per-call (not cached at construction) so the
// secret can be rotated without a redeploy — same rationale as
// routes.go's internalTokenAuth() reading APP_INTERNAL_TOKEN per-request.
// It MUST be Django's settings.SECRET_KEY (env DJANGO_SECRET_KEY): the state
// token this mints is verified by Django's still-live OAuthCallbackView, not
// by this backend — see oauth_state.go's package doc comment.
func oauthStateSecret() string { return os.Getenv("DJANGO_SECRET_KEY") }

func (u *usecase) OAuthStart(ctx context.Context, ownerID uuid.UUID, provider, typeKey string) (string, error) {
	if typeKey == "" {
		return "", httpx.BadRequest("credential.type_key_required", "type_key is required")
	}
	typ, ok := GetType(typeKey)
	if !ok {
		return "", httpx.BadRequest("credential.unknown_type", fmt.Sprintf("unknown type_key %q", typeKey))
	}
	if typ.Mode != ModeOAuth2 {
		return "", httpx.BadRequest("credential.not_oauth2", fmt.Sprintf("type_key %q is not an OAuth2 type", typeKey))
	}
	if typ.Provider != provider {
		return "", httpx.BadRequest("credential.provider_mismatch",
			fmt.Sprintf("type_key %q does not belong to provider %q", typeKey, provider))
	}
	prov, ok := getOAuthProvider(provider, u.oauth)
	if !ok {
		return "", httpx.NotFound("credential.unknown_provider", fmt.Sprintf("unknown provider %q", provider))
	}
	if prov.ClientID == "" {
		return "", httpx.Internal(fmt.Errorf("oauth provider %q: client id not configured", provider))
	}
	secret := oauthStateSecret()
	if secret == "" {
		return "", httpx.Internal(fmt.Errorf("oauth state signing: DJANGO_SECRET_KEY not set"))
	}
	nonce, err := newNonce()
	if err != nil {
		return "", httpx.Internal(err)
	}
	state := signState(secret, ownerID.String(), typeKey, nonce)

	q := url.Values{}
	q.Set("client_id", prov.ClientID)
	q.Set("redirect_uri", oauthCallbackURL(provider))
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(typ.Scopes, " "))
	q.Set("state", state)
	for k, v := range prov.ExtraParams {
		q.Set(k, v)
	}
	return prov.AuthorizeURL + "?" + q.Encode(), nil
}

func (u *usecase) decryptDetail(c *Credential) (*Detail, error) {
	data, err := u.decrypt(c.DataEncrypted)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return &Detail{Credential: *c, Data: data}, nil
}

func (u *usecase) encrypt(data map[string]any) (string, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal credential data: %w", err)
	}
	enc, err := u.cipher.Encrypt(raw)
	if err != nil {
		return "", fmt.Errorf("encrypt credential data: %w", err)
	}
	return enc, nil
}

func (u *usecase) decrypt(encoded string) (map[string]any, error) {
	raw, err := u.cipher.Decrypt(encoded)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential data: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("unmarshal credential data: %w", err)
	}
	return data, nil
}

// validateFields checks required fields and select-option membership,
// mirroring Django's _validate_data_against_fields. Returns a 400
// *httpx.AppError describing every problem found, or nil when valid.
func validateFields(fields []Field, data map[string]any) error {
	var problems []string
	for _, f := range fields {
		val, present := data[f.Name]
		if f.Required && (!present || isEmptyValue(val)) {
			problems = append(problems, f.Name+": required")
			continue
		}
		if present && f.Type == "select" && len(f.Options) > 0 {
			valid := false
			for _, opt := range f.Options {
				if opt.Value == fmt.Sprintf("%v", val) {
					valid = true
					break
				}
			}
			if !valid {
				problems = append(problems, fmt.Sprintf("%s: must be one of the declared options", f.Name))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return httpx.BadRequest("credential.invalid_data", strings.Join(problems, "; "))
}

func isEmptyValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

func mapNotFound(err error) error {
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("credential.not_found", "credential not found")
	}
	return httpx.Internal(err)
}
