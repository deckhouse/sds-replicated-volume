package suite

import "strings"

// TestId is the stable test identifier used in resource names.
// Deterministic by default so that failed cleanup causes name conflicts
// on the next run.
type TestId string

// ResourceName produces a deterministic resource name by joining the test ID
// with the provided identifiers: "{testId}-{ids joined with -}".
func (id TestId) ResourceName(ids ...string) string {
	return string(id) + "-" + strings.Join(ids, "-")
}
