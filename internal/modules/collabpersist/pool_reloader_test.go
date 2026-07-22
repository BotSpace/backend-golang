package collabpersist

import (
	"encoding/json"
	"testing"
)

func TestReplaceRuntimeFlowPreservesRuntimeMetadata(t *testing.T) {
	raw := `{"webhook_id":"hook","project_id":"project","replicas":2,"flow":{"nodes":[{"id":"old"}]},"env":{"RUNTIME_PRIMARY":"true"}}`
	next, err := replaceRuntimeFlow(raw, json.RawMessage(`{"nodes":[{"id":"new"}],"edges":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(next), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["webhook_id"] != "hook" || spec["replicas"] != float64(2) {
		t.Fatalf("runtime metadata changed: %#v", spec)
	}
	flow := spec["flow"].(map[string]any)
	nodes := flow["nodes"].([]any)
	if nodes[0].(map[string]any)["id"] != "new" {
		t.Fatalf("flow was not replaced: %#v", flow)
	}
}
