/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package drbdr

import (
	"context"
	"testing"
)

func TestCommandCache_CachesAndInvalidates(t *testing.T) {
	var calls int
	values := map[string]int{"a": 1, "b": 2}
	cache := newCommandCache(func(_ context.Context, name string) (*int, error) {
		calls++
		if v, ok := values[name]; ok {
			return &v, nil
		}
		return nil, nil
	})

	ctx := t.Context()

	if v, err := cache.Get(ctx, "a"); err != nil || v == nil || *v != 1 {
		t.Fatalf("Get(a) = %v, %v; want 1, nil", v, err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 after first miss", calls)
	}

	if v, err := cache.Get(ctx, "a"); err != nil || v == nil || *v != 1 {
		t.Fatalf("Get(a) hit = %v, %v; want 1, nil", v, err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (hit must not re-fetch)", calls)
	}

	cache.Invalidate("a")
	if _, err := cache.Get(ctx, "a"); err != nil {
		t.Fatalf("Get(a) after invalidate: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 after invalidate + get", calls)
	}
}

func TestCommandCache_CachesAbsent(t *testing.T) {
	var calls int
	cache := newCommandCache(func(_ context.Context, _ string) (*int, error) {
		calls++
		return nil, nil
	})

	ctx := t.Context()
	if v, err := cache.Get(ctx, "missing"); err != nil || v != nil {
		t.Fatalf("Get(missing) = %v, %v; want nil, nil", v, err)
	}
	if _, err := cache.Get(ctx, "missing"); err != nil {
		t.Fatalf("Get(missing) second: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (absent result must be cached)", calls)
	}
}

func TestCommandCache_InvalidateAll(t *testing.T) {
	var calls int
	cache := newCommandCache(func(_ context.Context, _ string) (*int, error) {
		calls++
		v := calls
		return &v, nil
	})

	ctx := t.Context()
	_, _ = cache.Get(ctx, "a")
	_, _ = cache.Get(ctx, "b")
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}

	cache.InvalidateAll()

	_, _ = cache.Get(ctx, "a")
	_, _ = cache.Get(ctx, "b")
	if calls != 4 {
		t.Fatalf("calls = %d, want 4 after InvalidateAll (both keys re-fetched)", calls)
	}
}

// TestCommandCache_InvalidateDuringFetch verifies that an invalidation that
// arrives while the command is running forces the next Get to re-fetch.
func TestCommandCache_InvalidateDuringFetch(t *testing.T) {
	var calls int
	var cache *commandCache[int]
	cache = newCommandCache(func(_ context.Context, _ string) (*int, error) {
		calls++
		if calls == 1 {
			cache.Invalidate("a")
		}
		v := calls
		return &v, nil
	})

	ctx := t.Context()
	if _, err := cache.Get(ctx, "a"); err != nil {
		t.Fatalf("Get(a): %v", err)
	}
	if _, err := cache.Get(ctx, "a"); err != nil {
		t.Fatalf("Get(a) second: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (invalidation during fetch must force a re-fetch)", calls)
	}
}
