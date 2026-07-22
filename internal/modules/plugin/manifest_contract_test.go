package plugin

import "testing"

func TestBuiltinManifestCanonicalRuntimeContracts(t *testing.T) {
	byType := make(map[string]map[string]any, len(builtinNodeManifests))
	for _, node := range builtinNodeManifests {
		if nodeType, ok := node["type"].(string); ok {
			byType[nodeType] = node
		}
	}

	assertHandles := func(nodeType string, expected ...string) {
		t.Helper()
		node := byType[nodeType]
		raw, ok := node["sourceHandles"].([]any)
		if !ok || len(raw) != len(expected) {
			t.Fatalf("%s sourceHandles = %#v, want %v", nodeType, node["sourceHandles"], expected)
		}
		for i, want := range expected {
			if got, _ := raw[i].(string); got != want {
				t.Fatalf("%s sourceHandles[%d] = %q, want %q", nodeType, i, got, want)
			}
		}
	}

	assertHandles("ElseIfNode", "true", "false")
	assertHandles("ElseNode", "true")

	callback := byType["CallbackQueryTriggerNode"]
	content, ok := callback["content"].([]any)
	if !ok {
		t.Fatal("CallbackQueryTriggerNode has no content")
	}
	var hasStaticOption, hasStaticValue bool
	for _, raw := range content {
		field, _ := raw.(map[string]any)
		switch field["key"] {
		case "state_type":
			for _, rawOption := range field["options"].([]any) {
				option, _ := rawOption.(map[string]any)
				if option["value"] == "static" {
					hasStaticOption = true
				}
			}
		case "state_value":
			visible, _ := field["visibleWhen"].(map[string]any)
			hasStaticValue = visible["key"] == "state_type" && visible["equals"] == "static"
		}
	}
	if !hasStaticOption || !hasStaticValue {
		t.Fatalf("callback static capture contract missing: option=%v value=%v", hasStaticOption, hasStaticValue)
	}

	for nodeType, node := range byType {
		content, _ := node["content"].([]any)
		hasKeyboardRows := false
		for _, raw := range content {
			field, _ := raw.(map[string]any)
			if field["type"] == "nested-rows" && field["key"] == "rows" {
				hasKeyboardRows = true
				break
			}
		}
		if hasKeyboardRows && node["buttonEdge"] != true {
			t.Errorf("%s renders keyboard rows but does not declare buttonEdge", nodeType)
		}
	}
}
