package driver

import "context"

// HealthCheck is the interface that must be implemented to be compatible with
// `HealthChecker`.
type HealthCheck interface {
	Name() string
	Check(ctx context.Context)
}

// HealthChecker helps with writing multi component health checkers.
type HealthChecker struct {
	checks []HealthCheck
}

// NewHealthChecker configures a new health checker with the passed in checks.
func NewHealthChecker(checks ...HealthCheck) *HealthChecker {
	return &HealthChecker{
		checks: checks,
	}
}
