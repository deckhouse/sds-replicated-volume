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

package debug

import (
	"encoding/json"
	"strings"
)

// LogEntry represents a parsed structured JSON log line from a controller or
// agent pod.
type LogEntry struct {
	Time        string
	Level       string
	Msg         string
	Controller  string
	Kind        string
	Name        string
	ReconcileID string
	Error       string

	// Phase data.
	// The logr→slog adapter stores the WithName chain as a flat top-level
	// "logger" field (slash-separated, e.g. "schedule-rvr/place-rvr/rvr-condition").
	// Phase-end attributes (result, changed, hasError, duration) are also
	// flat top-level fields emitted by flow.*.OnEnd.
	PhasePath []string
	PhaseName string
	Result    string
	Changed   string
	HasError  string
	Duration  string

	Extras []KV

	Raw map[string]any
}

// KV is a key-value pair for extra fields not covered by dedicated LogEntry
// fields. The optional File path enables clickable OSC 8 terminal hyperlinks.
type KV struct {
	Key  string
	Val  string
	File string
}

// knownKeys are standard fields extracted into dedicated LogEntry fields.
// Everything else goes into Extras.
var knownKeys = map[string]bool{
	"time": true, "level": true, "msg": true,
	"controller": true, "controllerGroup": true, "controllerKind": true,
	"namespace": true, "name": true, "reconcileID": true,
	"startedAt": true,
	"err":       true, "error": true,
	"logger": true,
	"result": true, "changed": true, "hasError": true, "duration": true,
	"requeueAfter": true, "phaseName": true,
}

// ParseLogEntry parses a JSON log line into a LogEntry. Returns nil if the
// line is not valid JSON. The snapshotsDir parameter controls where long
// values and hex dumps are saved; pass "" to disable file saving.
func ParseLogEntry(line, snapshotsDir string, kr *KindRegistry) *LogEntry {
	if kr == nil {
		kr = defaultKindRegistry
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return nil
	}

	e := &LogEntry{Raw: m}
	e.Time = strVal(m, "time")
	e.Level = strVal(m, "level")
	e.Msg = strVal(m, "msg")
	e.Controller = strVal(m, "controller")
	e.Kind = strVal(m, "controllerKind")
	e.Name = strVal(m, "name")
	e.ReconcileID = strVal(m, "reconcileID")

	e.Error = strVal(m, "err")
	if e.Error == "" {
		e.Error = strVal(m, "error")
	}

	if logger := strVal(m, "logger"); logger != "" {
		e.PhasePath = strings.Split(logger, "/")
		e.PhaseName = e.PhasePath[len(e.PhasePath)-1]
	}

	e.Result = strVal(m, "result")
	e.Changed = strVal(m, "changed")
	e.HasError = strVal(m, "hasError")
	e.Duration = strVal(m, "duration")

	for k, v := range m {
		if knownKeys[k] {
			continue
		}
		if k == e.Kind {
			continue
		}
		if k == "source" {
			if s, ok := v.(string); ok {
				s = strings.TrimPrefix(s, "kind source: ")
				e.Extras = append(e.Extras, KV{Key: "source", Val: s})
			}
			continue
		}
		e.Extras = append(e.Extras, processExtraValue(k, v, snapshotsDir, kr))
	}
	sortExtras(e.Extras)

	return e
}
