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

package drbdutils

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
)

func renderEvents2Line(e *Event) string {
	var b strings.Builder
	b.WriteString(e.Timestamp.Format(time.RFC3339Nano))
	b.WriteByte(' ')
	b.WriteString(string(e.Kind))
	b.WriteByte(' ')
	b.WriteString(string(e.Object))
	for _, k := range slices.Sorted(maps.Keys(e.State)) {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte(':')
		b.WriteString(e.State[k])
	}
	return b.String()
}

func respaceEvents2Line(line string) string {
	return "\t" + strings.Join(strings.Fields(line), " \t ") + "  "
}

func sameEvent(a, b *Event) bool {
	return a.Timestamp.Equal(b.Timestamp) && a.Kind == b.Kind && a.Object == b.Object && maps.Equal(a.State, b.State)
}

func FuzzParseEvents2Line(f *testing.F) {
	for _, seed := range []string{
		"",
		"2026-01-15T10:20:30.123456+03:00 exists -",
		"2026-01-15T10:20:30.123456+03:00 exists resource name:sdsrv-foo role:Secondary suspended:no force-io-failures:no may_promote:no promotion_score:10102",
		"2026-01-15T10:20:30.123456+03:00 exists device name:sdsrv-foo volume:0 minor:1000 backing_dev:/dev/vg-1/lv-1 disk:UpToDate client:no quorum:yes open:no",
		"2026-01-15T10:20:30.123456+03:00 exists connection name:sdsrv-foo peer-node-id:1 conn-name:node-b connection:Connected role:Secondary",
		"2026-01-15T10:20:30.123456+03:00 exists path name:sdsrv-foo peer-node-id:1 conn-name:node-b local:ipv4:10.12.1.19:7096 peer:ipv4:10.12.1.20:7096 established:yes",
		"2026-01-15T10:20:30.5Z change peer-device name:sdsrv-foo peer-node-id:1 conn-name:node-b volume:0 replication:SyncSource peer-disk:Inconsistent done:42.17",
		"2026-01-15T10:20:30Z destroy resource name:sdsrv-foo",
		"2026-01-15T10:20:30Z rename resource name:sdsrv-foo new_name:sdsrv-bar",
		"2026-01-15T10:20:30Z call helper name:sdsrv-foo peer-node-id:1 conn-name:node-b volume:0 helper:before-resync-target",
		"2026-01-15T10:20:30Z response helper name:sdsrv-foo helper:before-resync-target status:0",
		"2026-01-15T10:20:30Z create device name:sdsrv-foo volume:0 minor:1000 disk:Diskless",
		"2026-01-15T10:20:30Z change resource name:a name:b",
		"2026-01-15T10:20:30Z change resource :empty-key empty-value:",
		"2026-01-15T10:20:30Z change resource novalue",
		"not-a-timestamp exists resource name:x",
		"2026-01-15T10:20:30Z unknown resource name:x",
		"2026-01-15T10:20:30Z exists unknown name:x",
		"2026-01-15T10:20:30Z exists",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, line string) {
		res := parseLine(line)

		respaced := respaceEvents2Line(line)
		resRespaced := parseLine(respaced)

		switch r := res.(type) {
		case *UnparsedEvent:
			if r.RawEventLine != line {
				t.Fatalf("an unparsed event keeps %q instead of the line %q", r.RawEventLine, line)
			}
			if r.Err == nil {
				t.Fatalf("an unparsed event of %q carries no error", line)
			}
			if _, ok := resRespaced.(*UnparsedEvent); !ok {
				t.Fatalf("%q is rejected, but the same fields spaced differently, %q, parse as %+v", line, respaced, resRespaced)
			}
		case *Event:
			if _, ok := ParseEventKind(string(r.Kind)); !ok {
				t.Fatalf("parsed an unknown kind %q from %q", r.Kind, line)
			}
			if _, ok := ParseEventObject(string(r.Object)); !ok {
				t.Fatalf("parsed an unknown object %q from %q", r.Object, line)
			}
			if r.State == nil {
				t.Fatalf("parsed a nil state from %q", line)
			}
			if fields := len(strings.Fields(line)) - 3; len(r.State) > fields {
				t.Fatalf("parsed %d state keys from %d key-value fields of %q", len(r.State), fields, line)
			}
			for k, v := range r.State {
				if strings.Contains(k, ":") || strings.ContainsFunc(k+v, unicode.IsSpace) {
					t.Fatalf("parsed a key %q with value %q, which cannot come from one key:value field of %q", k, v, line)
				}
			}

			e, ok := resRespaced.(*Event)
			if !ok || !sameEvent(r, e) {
				t.Fatalf("spacing changed the event: %+v from %q, %+v from %q", r, line, resRespaced, respaced)
			}

			rendered := renderEvents2Line(r)
			again, ok := parseLine(rendered).(*Event)
			if !ok {
				t.Fatalf("the event %+v parsed from %q, rendered back as %q, does not parse", r, line, rendered)
			}
			if !sameEvent(r, again) {
				t.Fatalf("the event changed on a round trip: %+v from %q, then %+v from %q", r, line, again, rendered)
			}
		default:
			t.Fatalf("parseLine(%q) returned %T", line, res)
		}
	})
}
