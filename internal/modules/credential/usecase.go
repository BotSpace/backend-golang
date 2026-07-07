package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

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
	Options(ctx context.Context, in OptionsInput) ([]OptionItem, error)

	// InternalGetByID resolves a credential by id for the engine. projectID,
	// when non-nil, must own the credential (cross-tenant guard) — mirrors
	// core.apps.api.views.internal.credential_by_id.
	InternalGetByID(ctx context.Context, id uuid.UUID, projectID *uuid.UUID) (*Detail, error)
	// InternalGetByName resolves a credential by its owner-unique name, with
	// the owner derived from projectID — mirrors credential_by_name.
	InternalGetByName(ctx context.Context, name string, projectID uuid.UUID) (*Detail, error)

	// OAuthStart begins a browser OAuth2 consent flow for an oauth2-mode
	// credential type — mirrors OAuthStartView.post. Returns the Google
	// consent URL to redirect the user's browser to; OAuthCallback below
	// completes the flow once Google redirects back.
	OAuthStart(ctx context.Context, ownerID uuid.UUID, provider, typeKey string) (authorizeURL string, err error)

	// OAuthCallback completes an OAuth2 consent flow — mirrors Django's
	// OAuthCallbackView.get. code/state/errorParam are the raw query params
	// Google redirected the browser with; there is no Authorization header
	// (the caller is the user's browser, not the dashboard), so the caller
	// is authenticated purely via the signed state token. Returns nil on
	// success (the credential has been created/updated for the state's
	// owner); a non-nil *httpx.AppError otherwise (the handler renders it
	// into the callback's HTML response, never a JSON error body).
	OAuthCallback(ctx context.Context, provider, code, state, errorParam string) error
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
// It MUST be Django's settings.SECRET_KEY (env DJANGO_SECRET_KEY): kept
// Django-signing-compatible even though both OAuthStart and OAuthCallback
// now live on this backend — see oauth_state.go's package doc comment.
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
	q.Set("redirect_uri", oauthCallbackURL(u.oauth.CallbackBaseURL, provider))
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(typ.Scopes, " "))
	q.Set("state", state)
	for k, v := range prov.ExtraParams {
		q.Set(k, v)
	}
	return prov.AuthorizeURL + "?" + q.Encode(), nil
}

func (u *usecase) OAuthCallback(ctx context.Context, provider, code, state, errorParam string) error {
	if errorParam != "" {
		return httpx.BadRequest("credential.oauth_denied", fmt.Sprintf("provider returned error: %s", errorParam))
	}
	if code == "" || state == "" {
		return httpx.BadRequest("credential.oauth_missing_params", "missing code or state parameter")
	}

	secret := oauthStateSecret()
	if secret == "" {
		return httpx.Internal(fmt.Errorf("oauth state verification: DJANGO_SECRET_KEY not set"))
	}
	ownerIDStr, typeKey, err := verifyState(secret, state, stateMaxAgeSeconds*time.Second)
	if err != nil {
		return httpx.BadRequest("credential.oauth_bad_state", "state token expired or invalid — please try connecting again")
	}
	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		return httpx.BadRequest("credential.oauth_bad_state", "invalid state token")
	}

	typ, ok := GetType(typeKey)
	if !ok || typ.Provider != provider {
		return httpx.BadRequest("credential.oauth_invalid_type", fmt.Sprintf("invalid type_key for provider %q", provider))
	}
	prov, ok := getOAuthProvider(provider, u.oauth)
	if !ok {
		return httpx.NotFound("credential.unknown_provider", fmt.Sprintf("unknown provider %q", provider))
	}
	if prov.ClientID == "" || prov.ClientSecret == "" {
		return httpx.Internal(fmt.Errorf("oauth provider %q: client id/secret not configured", provider))
	}

	client := u.oauth.httpClient()
	redirectURI := oauthCallbackURL(u.oauth.CallbackBaseURL, provider)
	tokenResp, err := exchangeOAuthCode(ctx, client, prov, redirectURI, code)
	if err != nil {
		return httpx.Internal(fmt.Errorf("oauth token exchange: %w", err))
	}

	// Userinfo (account email) is best-effort — Django ignores fetch errors
	// too, since the credential is still usable without it (just falls back
	// to the "first credential of this type" upsert match instead of the
	// email-scoped one).
	var userinfo map[string]any
	if accessToken, _ := tokenResp["access_token"].(string); accessToken != "" && prov.UserinfoURL != "" {
		userinfo, _ = fetchOAuthUserinfo(ctx, client, prov, accessToken)
	}

	tokenData := buildOAuthTokenData(tokenResp, userinfo)
	if err := u.upsertOAuthCredential(ctx, ownerID, typeKey, typ.Label, tokenData); err != nil {
		return httpx.Internal(fmt.Errorf("save oauth credential: %w", err))
	}
	return nil
}

// upsertOAuthCredential mirrors Django's OAuthCallbackView._upsert_credential:
// look up an existing credential for (ownerID, typeKey), narrowed by
// account_email when known (name pattern "<Label> (<email>)"), else fall
// back to the first credential of that type. Found -> refresh its token data
// via the same Update path a manual PATCH would use; not found -> Create a
// new one with a de-duplicated default name, same as the owner-facing Create
// path.
func (u *usecase) upsertOAuthCredential(ctx context.Context, ownerID uuid.UUID, typeKey, label string, tokenData map[string]any) error {
	accountEmail, _ := tokenData["account_email"].(string)

	existing, err := u.repo.List(ctx, ListFilter{OwnerID: ownerID, TypeKey: typeKey, Limit: 500})
	if err != nil {
		return fmt.Errorf("list existing credentials: %w", err)
	}

	var match *Credential
	if accountEmail != "" {
		expectedName := fmt.Sprintf("%s (%s)", label, accountEmail)
		for i := range existing {
			if existing[i].Name == expectedName {
				match = &existing[i]
				break
			}
		}
	} else if len(existing) > 0 {
		match = &existing[0]
	}

	if match != nil {
		_, err := u.Update(ctx, match.ID, ownerID, UpdateData{Data: tokenData})
		return err
	}

	existingNames := make(map[string]bool, len(existing))
	allForOwner, err := u.repo.List(ctx, ListFilter{OwnerID: ownerID, Limit: 500})
	if err != nil {
		return fmt.Errorf("list owner credentials: %w", err)
	}
	for _, c := range allForOwner {
		existingNames[c.Name] = true
	}

	baseName := label
	if accountEmail != "" {
		baseName = fmt.Sprintf("%s (%s)", label, accountEmail)
	}
	name := baseName
	for n := 1; existingNames[name]; n++ {
		name = fmt.Sprintf("%s (%d)", baseName, n)
	}

	_, err = u.Create(ctx, CreateData{OwnerID: ownerID, TypeKey: typeKey, Name: name, Data: tokenData})
	return err
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
