package credential

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/httpx"
)

const (
	googleSheetsAPIBase = "https://sheets.googleapis.com"
	googleDriveAPIBase  = "https://www.googleapis.com/drive/v3"
)

func (u *usecase) Options(ctx context.Context, in OptionsInput) ([]OptionItem, error) {
	switch in.Resource {
	case "google_spreadsheets", "google_sheet_tabs", "google_sheet_columns":
		return u.googleSheetsOptions(ctx, in)
	default:
		return nil, httpx.NotFound("credential.options_not_found", "options resource not found")
	}
}

func (u *usecase) googleSheetsOptions(ctx context.Context, in OptionsInput) ([]OptionItem, error) {
	d, err := u.Get(ctx, in.CredentialID, in.OwnerID)
	if err != nil {
		return nil, err
	}
	if d.TypeKey != "google_sheets" {
		return nil, httpx.BadRequest("credential.invalid_type", "credential must be google_sheets")
	}
	accessToken, err := u.oauthAccessToken(ctx, d)
	if err != nil {
		return nil, err
	}

	switch in.Resource {
	case "google_spreadsheets":
		return u.listGoogleSpreadsheets(ctx, accessToken)
	case "google_sheet_tabs":
		spreadsheetID := strings.TrimSpace(in.Params["spreadsheet_id"])
		if spreadsheetID == "" {
			return nil, httpx.BadRequest("credential.missing_spreadsheet_id", "spreadsheet_id is required")
		}
		return u.listGoogleSheetTabs(ctx, accessToken, spreadsheetID)
	case "google_sheet_columns":
		spreadsheetID := strings.TrimSpace(in.Params["spreadsheet_id"])
		sheet := strings.TrimSpace(in.Params["sheet"])
		if spreadsheetID == "" || sheet == "" {
			return nil, httpx.BadRequest("credential.missing_sheet_params", "spreadsheet_id and sheet are required")
		}
		return u.listGoogleSheetColumns(ctx, accessToken, spreadsheetID, sheet)
	default:
		return nil, httpx.NotFound("credential.options_not_found", "options resource not found")
	}
}

func (u *usecase) oauthAccessToken(ctx context.Context, d *Detail) (string, error) {
	token, _ := d.Data["access_token"].(string)
	expiresAt, _ := d.Data["expires_at"].(string)
	if token != "" && !oauthTokenExpired(expiresAt) {
		return token, nil
	}
	refreshToken, _ := d.Data["refresh_token"].(string)
	if refreshToken == "" {
		if token != "" {
			return token, nil
		}
		return "", httpx.BadRequest("credential.oauth_token_missing", "OAuth access token is missing")
	}
	refreshed, err := u.refreshOAuthToken(ctx, refreshToken)
	if err != nil {
		if token != "" {
			return token, nil
		}
		return "", httpx.Internal(fmt.Errorf("refresh oauth token: %w", err))
	}
	for k, v := range d.Data {
		if _, exists := refreshed[k]; !exists {
			refreshed[k] = v
		}
	}
	if refreshed["refresh_token"] == "" {
		refreshed["refresh_token"] = refreshToken
	}
	updated, err := u.Update(ctx, d.ID, d.OwnerID, UpdateData{Data: refreshed})
	if err != nil {
		return "", err
	}
	token, _ = updated.Data["access_token"].(string)
	if token == "" {
		return "", httpx.BadRequest("credential.oauth_token_missing", "OAuth access token is missing")
	}
	return token, nil
}

func oauthTokenExpired(expiresAt string) bool {
	if expiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return false
	}
	return time.Now().UTC().After(t.Add(-2 * time.Minute))
}

func (u *usecase) refreshOAuthToken(ctx context.Context, refreshToken string) (map[string]any, error) {
	prov, ok := getOAuthProvider("google", u.oauth)
	if !ok {
		return nil, fmt.Errorf("google oauth provider not configured")
	}
	if prov.ClientID == "" || prov.ClientSecret == "" {
		return nil, fmt.Errorf("google oauth client id/secret not configured")
	}
	form := url.Values{}
	form.Set("client_id", prov.ClientID)
	form.Set("client_secret", prov.ClientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResp, err := doOAuthJSONRequest(u.oauth.httpClient(), req)
	if err != nil {
		return nil, err
	}
	return buildOAuthTokenData(tokenResp, nil), nil
}

func (u *usecase) listGoogleSpreadsheets(ctx context.Context, accessToken string) ([]OptionItem, error) {
	q := url.Values{}
	q.Set("q", "mimeType='application/vnd.google-apps.spreadsheet' and trashed=false")
	q.Set("fields", "files(id,name)")
	q.Set("pageSize", "100")
	reqURL := googleDriveAPIBase + "/files?" + q.Encode()

	var out struct {
		Files []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"files"`
	}
	if err := u.doGoogleJSON(ctx, accessToken, http.MethodGet, reqURL, nil, &out); err != nil {
		return nil, err
	}
	options := make([]OptionItem, 0, len(out.Files))
	for _, f := range out.Files {
		if f.ID == "" {
			continue
		}
		options = append(options, OptionItem{Value: f.ID, Label: firstNonEmpty(f.Name, f.ID)})
	}
	return options, nil
}

func (u *usecase) listGoogleSheetTabs(ctx context.Context, accessToken, spreadsheetID string) ([]OptionItem, error) {
	reqURL := googleSheetsAPIBase + "/v4/spreadsheets/" + url.PathEscape(spreadsheetID) + "?fields=sheets.properties.title"
	var out struct {
		Sheets []struct {
			Properties struct {
				Title string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if err := u.doGoogleJSON(ctx, accessToken, http.MethodGet, reqURL, nil, &out); err != nil {
		return nil, err
	}
	options := make([]OptionItem, 0, len(out.Sheets))
	for _, s := range out.Sheets {
		title := s.Properties.Title
		if title == "" {
			continue
		}
		options = append(options, OptionItem{Value: title, Label: title})
	}
	return options, nil
}

func (u *usecase) listGoogleSheetColumns(ctx context.Context, accessToken, spreadsheetID, sheet string) ([]OptionItem, error) {
	readRange := quoteSheetName(sheet) + "!1:1"
	reqURL := googleSheetsAPIBase + "/v4/spreadsheets/" + url.PathEscape(spreadsheetID) + "/values/" + url.PathEscape(readRange)
	var out struct {
		Values [][]string `json:"values"`
	}
	if err := u.doGoogleJSON(ctx, accessToken, http.MethodGet, reqURL, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Values) == 0 {
		return []OptionItem{}, nil
	}
	options := make([]OptionItem, 0, len(out.Values[0]))
	for _, col := range out.Values[0] {
		col = strings.TrimSpace(col)
		if col == "" {
			continue
		}
		options = append(options, OptionItem{Value: col, Label: col})
	}
	return options, nil
}

func (u *usecase) doGoogleJSON(ctx context.Context, accessToken, method, reqURL string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return httpx.Internal(fmt.Errorf("marshal google request: %w", err))
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
	if err != nil {
		return httpx.Internal(fmt.Errorf("build google request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := u.oauth.httpClient().Do(req)
	if err != nil {
		return httpx.Internal(fmt.Errorf("google request: %w", err))
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return httpx.NotFound("credential.google_not_found", "Google resource not found")
	}
	if resp.StatusCode == http.StatusForbidden {
		return httpx.Forbidden("credential.google_forbidden", "Google resource access denied")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return httpx.Unauthorized("credential.google_unauthorized", "Google credential is unauthorized")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpx.Internal(fmt.Errorf("google returned %d: %s", resp.StatusCode, truncate(string(respBody), 500)))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return httpx.Internal(fmt.Errorf("decode google response: %w", err))
	}
	return nil
}

func quoteSheetName(sheet string) string {
	if sheet == "" {
		return sheet
	}
	escaped := strings.ReplaceAll(sheet, "'", "''")
	return "'" + escaped + "'"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
