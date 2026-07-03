package plugin

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

//go:embed data/builtin_node_manifests.json
var builtinManifestsJSON []byte

// builtinNodeManifests is the canonical built-in node catalog (Django
// data/builtin_node_manifests.json), parsed once at package init. A corrupt
// embedded file is a build-time bug, so it panics loudly instead of degrading
// silently at request time (mirrors Django's @lru_cache read).
var builtinNodeManifests = mustParseBuiltinManifests()

func mustParseBuiltinManifests() []map[string]any {
	var nodes []map[string]any
	if err := json.Unmarshal(builtinManifestsJSON, &nodes); err != nil {
		panic(fmt.Sprintf("plugin: invalid embedded builtin_node_manifests.json: %v", err))
	}
	return nodes
}

// BuiltinNodeManifestsJSON returns the raw embedded builtin_node_manifests.json
// bytes (the canonical Django core/apps/api/data/builtin_node_manifests.json
// copy, byte-for-byte). Exported so other modules — namely config's
// GET /shared/manifest/builtin-nodes/ (agentv3's global node-catalog boot
// fetch) — can reuse the single embedded copy instead of duplicating this
// ~8000-line file.
func BuiltinNodeManifestsJSON() []byte {
	return builtinManifestsJSON
}

// allowedFieldTypes mirrors Django's ALLOWED_FIELD_TYPES — a module manifest
// declaring an unknown field type is rejected rather than silently breaking
// the constructor UI.
var allowedFieldTypes = map[string]bool{
	"text": true, "number": true, "textarea": true, "select": true,
	"checkbox": true, "switch": true, "description": true, "widget": true,
	"json": true, "color": true, "file": true, "datetime": true,
	"boolean": true, "code": true, "credential": true,
}

// rpcEnvelope is the module JSON-RPC 2.0 response shape ({"result": ...}).
type rpcEnvelope struct {
	Result json.RawMessage `json:"result"`
}

// httpManifestFetcher is the default ManifestFetcher: it POSTs `describe` and
// `docs` JSON-RPC calls to the module's endpoint (Django module_service.fetch_manifest).
type httpManifestFetcher struct {
	client *http.Client
}

// NewHTTPManifestFetcher builds the production ManifestFetcher.
func NewHTTPManifestFetcher() ManifestFetcher {
	return &httpManifestFetcher{client: &http.Client{Timeout: 8 * time.Second}}
}

func (f *httpManifestFetcher) Fetch(ctx context.Context, slug, endpoint, authToken string) (json.RawMessage, error) {
	describe, err := f.call(ctx, endpoint, authToken, "describe")
	if err != nil {
		return nil, fmt.Errorf("describe call failed: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(describe, &result); err != nil {
		return nil, fmt.Errorf("describe response is not a JSON object: %w", err)
	}

	for _, n := range asSlice(result["nodes"]) {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		for _, c := range asSlice(node["content"]) {
			field, ok := c.(map[string]any)
			if !ok {
				continue
			}
			ft, _ := field["type"].(string)
			if ft != "" && !allowedFieldTypes[ft] {
				return nil, fmt.Errorf("module %s: unknown field type %q in node %q", slug, ft, node["type"])
			}
		}
	}

	// credentialTypes: the module declares its own credential schemas; keys
	// must be namespaced "{slug}." to avoid collisions with other modules.
	cleaned := make([]any, 0)
	for _, c := range asSlice(result["credentialTypes"]) {
		ct, ok := c.(map[string]any)
		if !ok {
			continue
		}
		key, _ := ct["key"].(string)
		if strings.HasPrefix(key, slug+".") {
			cleaned = append(cleaned, ct)
		}
	}
	result["credential_types"] = cleaned

	if docs, err := f.call(ctx, endpoint, authToken, "docs"); err == nil {
		var docsResult map[string]any
		if json.Unmarshal(docs, &docsResult) == nil {
			if md, ok := docsResult["markdown"].(string); ok {
				result["docs"] = md
			}
		}
	}
	if _, ok := result["docs"]; !ok {
		result["docs"] = ""
	}

	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return out, nil
}

func (f *httpManifestFetcher) call(ctx context.Context, endpoint, authToken, method string) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": method, "params": map[string]any{}, "id": 1,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: unexpected status %d", endpoint, resp.StatusCode)
	}

	var env rpcEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Result) == 0 {
		// A minimal module server may return the payload unwrapped.
		return raw, nil
	}
	return env.Result, nil
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}
