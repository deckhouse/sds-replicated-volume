package envtesting

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestId is the stable test identifier used in resource names.
// Deterministic by default so that failed cleanup causes name conflicts
// on the next run.
type TestId string

// ResourceName produces a deterministic resource name by joining the test ID
// with the provided identifiers: "{testId}-{ids joined with -}".
func (id TestId) ResourceName(ids ...string) string {
	return string(id) + "-" + strings.Join(ids, "-")
}

// E is the environment test context. It wraps *testing.T with a
// timeout-aware context and per-helper configuration from a JSON config file.
type E struct {
	*testing.T
	ctx      context.Context
	sections map[string]json.RawMessage
}

// Discover reads the JSON config file and returns a new *E. The file path is
// read from the E2E_CONFIG_PATH environment variable; if unset, defaults to
// ".env.json".
func Discover(t *testing.T) *E {
	path := os.Getenv("E2E_CONFIG_PATH")
	if path == "" {
		path = ".env.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config from %s: %v", path, err)
	}

	var sections map[string]json.RawMessage
	if err := json.Unmarshal(data, &sections); err != nil {
		t.Fatalf("parsing config sections from %s: %v", path, err)
	}

	return &E{
		T:        t,
		ctx:      t.Context(),
		sections: sections,
	}
}

// Context returns the context for the current test scope. Inside
// RunWithTimeout, this context carries the specified deadline.
func (e *E) Context() context.Context {
	return e.ctx
}

// Options unmarshals the config section whose key matches the Go type name of
// *target. target must be a pointer to a named type (struct, string alias,
// etc.). Each call unmarshals from the raw JSON, so callers always get an
// independent copy.
func (e *E) Options(target any) {
	typ := reflect.TypeOf(target)
	if typ.Kind() != reflect.Pointer {
		e.Fatalf("target must be a pointer, got %T", target)
	}

	elemType := typ.Elem()
	typeName := elemType.Name()
	if typeName == "" {
		e.Fatalf("target must be a pointer to a named type, got %T", target)
	}

	raw, ok := e.sections[typeName]
	if !ok {
		e.Fatalf("section %q not found", typeName)
	}

	if err := json.Unmarshal(raw, target); err != nil {
		e.Fatalf("unmarshalling section %q: %v", typeName, err)
	}
}

// Run runs fn as a subtest of e. The child *E shares config sections with
// the parent and wraps the subtest's *testing.T.
func (e *E) Run(name string, fn func(*E)) bool {
	return e.T.Run(name, func(t *testing.T) {
		child := &E{
			T:        t,
			ctx:      t.Context(),
			sections: e.sections,
		}
		fn(child)
	})
}

// RunWithTimeout runs fn as a subtest of e with a deadline context.
// e.Context() inside fn returns a context that will be cancelled after d.
func (e *E) RunWithTimeout(name string, d time.Duration, fn func(*E)) bool {
	return e.T.Run(name, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), d)
		t.Cleanup(cancel)
		child := &E{
			T:        t,
			ctx:      ctx,
			sections: e.sections,
		}
		fn(child)
	})
}
