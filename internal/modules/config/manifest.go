package config

import (
	"encoding/json"
	"fmt"

	"github.com/botspace/backend/internal/modules/plugin"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
)

// builtinNodeManifestsResponse is the shape GET /shared/manifest/builtin-nodes/
// returns — mirrors core/apps/shared/views/manifest.py's
// `Response({"manifests": get_builtin_node_manifests()})`.
type builtinNodeManifestsResponse struct {
	Manifests []json.RawMessage `json:"manifests"`
	// Categories: {category_key: {label, doc}} — category-level docs so the
	// agent shows a short description per category and lazy-loads a category's
	// nodes on demand. Sourced from plugin's embedded node_categories.json.
	Categories json.RawMessage `json:"categories,omitempty"`
}

// builtinNodeManifests is parsed once at package init from plugin's embedded
// copy of Django's canonical data/builtin_node_manifests.json — this module
// deliberately does not keep its own copy of the (~8000-line) file.
var builtinNodeManifests = mustParseBuiltinNodeManifests()

// nodeCategories is the raw category-docs map, validated once at init.
var nodeCategories = mustParseNodeCategories()

func mustParseBuiltinNodeManifests() []json.RawMessage {
	var manifests []json.RawMessage
	if err := json.Unmarshal(plugin.BuiltinNodeManifestsJSON(), &manifests); err != nil {
		panic(fmt.Sprintf("config: invalid embedded builtin_node_manifests.json: %v", err))
	}
	return manifests
}

func mustParseNodeCategories() json.RawMessage {
	raw := plugin.NodeCategoriesJSON()
	if !json.Valid(raw) {
		panic("config: invalid embedded node_categories.json")
	}
	return json.RawMessage(raw)
}

// BuiltinNodeManifests godoc
// @Summary  Built-in node catalog (global, project-less)
// @Tags     shared-manifest
// @Produce  json
// @Success  200  {object}  builtinNodeManifestsResponse
// @Router   /shared/manifest/builtin-nodes/ [get]
//
// BuiltinNodeManifests serves the same canonical node catalog Django exposed
// at this path (AllowAny — built-in manifests aren't secret, the constructor
// already ships them to the browser). agentv3
// (../agent/src/agent/flow/manifest_compiler/loader.py) fetches this
// synchronously at import time when it isn't running in the monorepo layout
// (no local `backend/core/apps/api/data/builtin_node_manifests.json` sibling
// file to read directly) — a 404 here crashes the agent process at boot.
func (h *Handler) BuiltinNodeManifests(c *gin.Context) {
	httpx.OK(c, 200, builtinNodeManifestsResponse{
		Manifests:  builtinNodeManifests,
		Categories: nodeCategories,
	})
}
