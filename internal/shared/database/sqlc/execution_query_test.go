package sqlcdb

import (
	"strings"
	"testing"
)

// PostgreSQL cannot infer one parameter as both timestamptz and the untyped
// result of a CASE expression. Keep the explicit casts that production ledger
// writes need; sqlc generation alone does not detect this prepare-time error.
func TestExecutionTimestampParametersAreExplicitlyTyped(t *testing.T) {
	for name, query := range map[string]string{
		"finish execution": finishExecution,
		"node run":         upsertExecutionNodeRun,
	} {
		if !strings.Contains(query, "::timestamptz") {
			t.Fatalf("%s query must explicitly cast event timestamps", name)
		}
	}
}
