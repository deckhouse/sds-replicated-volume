package etesting

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

const defaultTestID = "e2e"

// E is the e2e test context. It embeds *testing.T and adds test-wide
// configuration: a stable test ID and per-helper options sections discovered
// from a JSON config file.
type E struct {
	*testing.T
	testID   string
	sections map[string]json.RawMessage
	cache    map[string]any
}

// New discovers e2e test configuration from a JSON config file and returns a
// new *E. The file path is read from the E2E_CONFIG_PATH environment variable;
// if unset, defaults to ".env.json".
func New(t *testing.T) *E {
	path := os.Getenv("E2E_CONFIG_PATH")
	if path == "" {
		path = ".env.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config from %s: %v", path, err)
	}

	// Parse top-level fields.
	var top struct {
		TestID string `json:"testId"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("parsing config top-level from %s: %v", path, err)
	}

	testID := top.TestID
	if testID == "" {
		testID = defaultTestID
	}

	// Parse all sections as raw JSON for on-demand unmarshalling.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parsing config sections from %s: %v", path, err)
	}

	// Remove top-level keys so they don't clash with options type names.
	delete(raw, "testId")

	return &E{
		T:        t,
		testID:   testID,
		sections: raw,
		cache:    make(map[string]any),
	}
}

// TestID returns a stable test identifier used in resource names.
// Deterministic by default so that failed cleanup causes name conflicts
// on the next run.
func (e *E) TestID() string {
	return e.testID
}

// Options unmarshals the config section into target. The section key is derived
// from the Go type name of *target via reflection (e.g., *DRBDResourcesOptions
// looks up section "DRBDResourcesOptions"). Results are cached; subsequent calls
// for the same type return a deep copy without re-unmarshalling.
func (e *E) Options(target any) {
	typ := reflect.TypeOf(target)
	if typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct {
		e.Fatalf("Options: target must be a pointer to struct, got %T", target)
	}
	typeName := typ.Elem().Name()

	if cached, ok := e.cache[typeName]; ok {
		// Deep copy via JSON round-trip.
		data, err := json.Marshal(cached)
		if err != nil {
			e.Fatalf("Options: marshalling cached %q: %v", typeName, err)
		}
		if err := json.Unmarshal(data, target); err != nil {
			e.Fatalf("Options: unmarshalling cached %q: %v", typeName, err)
		}
		return
	}

	raw, ok := e.sections[typeName]
	if !ok {
		e.Fatalf("config: section %q not found", typeName)
	}

	if err := json.Unmarshal(raw, target); err != nil {
		e.Fatalf("config: unmarshalling section %q: %v", typeName, err)
	}

	// Store a deep copy in cache.
	cacheValue := reflect.New(typ.Elem()).Interface()
	cacheData, err := json.Marshal(target)
	if err != nil {
		e.Fatalf("Options: marshalling for cache %q: %v", typeName, err)
	}
	if err := json.Unmarshal(cacheData, cacheValue); err != nil {
		e.Fatalf("Options: unmarshalling for cache %q: %v", typeName, err)
	}
	e.cache[typeName] = cacheValue
}

// Run runs fn as a subtest of e. The child *E shares testID, config sections,
// and cache with the parent, but wraps the subtest's *testing.T.
func (e *E) Run(name string, fn func(*E)) bool {
	return e.T.Run(name, func(t *testing.T) {
		child := &E{
			T:        t,
			testID:   e.testID,
			sections: e.sections,
			cache:    e.cache,
		}
		fn(child)
	})
}
