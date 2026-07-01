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
	"fmt"
	"strings"
)

// LongValueThreshold is the character count above which a value is saved to a
// file instead of displayed inline.
const LongValueThreshold = 120

// ProcessExtraValue converts a raw JSON field into a KV for display using the
// default kind registry.
func ProcessExtraValue(key string, raw any, snapshotsDir string) KV {
	return processExtraValue(key, raw, snapshotsDir, defaultKindRegistry)
}

func processExtraValue(key string, raw any, snapshotsDir string, kr *KindRegistry) KV {
	switch val := raw.(type) {
	case map[string]any:
		return processMapValue(key, val, snapshotsDir, kr)
	case []any:
		b, _ := json.Marshal(val)
		s := string(b)
		if len(s) <= LongValueThreshold {
			return KV{Key: key, Val: s}
		}
		path := SaveLogBody(snapshotsDir, key, b)
		return KV{Key: key, Val: fmt.Sprintf("[%d items]", len(val)), File: path}
	case string:
		if IsHexDump(val) {
			return processHexDumpValue(key, val, snapshotsDir, kr)
		}
		if len(val) > LongValueThreshold && len(val) > 2 && val[0] == '{' {
			var obj map[string]any
			if json.Unmarshal([]byte(val), &obj) == nil {
				return processMapValue(key, obj, snapshotsDir, kr)
			}
		}
		if len(val) <= LongValueThreshold {
			return KV{Key: key, Val: val}
		}
		path := SaveLogBody(snapshotsDir, key, []byte(val))
		return KV{Key: key, Val: val[:LongValueThreshold] + "…", File: path}
	default:
		return KV{Key: key, Val: fmtVal(raw)}
	}
}

// ProcessMapValue handles a JSON object using the default kind registry.
func ProcessMapValue(key string, obj map[string]any, snapshotsDir string) KV {
	return processMapValue(key, obj, snapshotsDir, defaultKindRegistry)
}

func processMapValue(key string, obj map[string]any, snapshotsDir string, kr *KindRegistry) KV {
	if brief := briefKubeResource(obj, kr); brief != "" {
		b, _ := json.MarshalIndent(obj, "", "  ")
		var path string
		if len(b) > LongValueThreshold {
			path = SaveLogBody(snapshotsDir, key, b)
		}
		return KV{Key: key, Val: brief, File: path}
	}

	compact, _ := json.Marshal(obj)
	s := string(compact)
	if len(s) <= LongValueThreshold {
		return KV{Key: key, Val: s}
	}
	pretty, _ := json.MarshalIndent(obj, "", "  ")
	path := SaveLogBody(snapshotsDir, key, pretty)
	return KV{Key: key, Val: fmt.Sprintf("{%d fields}", len(obj)), File: path}
}

// ProcessHexDumpValue handles a hexdump string using the default kind registry.
func ProcessHexDumpValue(key, hexStr, snapshotsDir string) KV {
	return processHexDumpValue(key, hexStr, snapshotsDir, defaultKindRegistry)
}

func processHexDumpValue(key, hexStr, snapshotsDir string, kr *KindRegistry) KV {
	raw := ParseHexDump(hexStr)

	var brief string
	switch {
	case len(raw) >= 4 && string(raw[:4]) == k8sProtobufMagic:
		apiVersion, k := ExtractK8sProtobufMeta(raw)
		sk := kr.ShortFor(k)
		switch {
		case k != "" && apiVersion != "":
			brief = fmt.Sprintf("pb %s (%s) %d bytes", sk, apiVersion, len(raw))
		case k != "":
			brief = fmt.Sprintf("pb %s %d bytes", sk, len(raw))
		default:
			brief = fmt.Sprintf("pb %d bytes", len(raw))
		}
	case len(raw) > 0:
		brief = fmt.Sprintf("binary %d bytes", len(raw))
	default:
		brief = fmt.Sprintf("hexdump %d chars", len(hexStr))
	}

	path := SaveLogBody(snapshotsDir, key, []byte(hexStr))
	return KV{Key: key, Val: brief, File: path}
}

// BriefKubeResource returns a brief description using the default kind registry.
func BriefKubeResource(obj map[string]any) string {
	return briefKubeResource(obj, defaultKindRegistry)
}

func briefKubeResource(obj map[string]any, kr *KindRegistry) string {
	kind, _ := obj["kind"].(string)
	if kind == "" {
		return ""
	}
	meta, _ := obj["metadata"].(map[string]any)
	if meta == nil {
		return ""
	}

	if kind == "Status" {
		reason, _ := obj["reason"].(string)
		code, _ := obj["code"].(float64)

		var target string
		if details, ok := obj["details"].(map[string]any); ok {
			dKind, _ := details["kind"].(string)
			dName, _ := details["name"].(string)
			if dKind != "" && dName != "" {
				target = kr.ShortFor(dKind) + "/" + dName
			}
		}

		var parts []string
		if code != 0 {
			parts = append(parts, fmt.Sprintf("%d", int(code)))
		}
		if reason != "" {
			parts = append(parts, reason)
		}
		if target != "" {
			parts = append(parts, target)
		}
		if len(parts) == 0 {
			return "Status"
		}
		return "Status " + strings.Join(parts, " ")
	}

	name, _ := meta["name"].(string)
	ns, _ := meta["namespace"].(string)
	rv, _ := meta["resourceVersion"].(string)

	displayKind := kr.ShortFor(kind)

	var ident string
	switch {
	case ns != "":
		ident = fmt.Sprintf("%s/%s/%s", displayKind, ns, name)
	case name != "":
		ident = fmt.Sprintf("%s/%s", displayKind, name)
	default:
		ident = displayKind
	}

	if statusMap, ok := obj["status"].(map[string]any); ok && statusMap != nil {
		if _, hasSpec := obj["spec"]; !hasSpec {
			ident += "/status"
		}
	}

	if rv != "" {
		return fmt.Sprintf("%s rv=%s", ident, rv)
	}
	return ident
}

const fmtValMaxLen = 200

// fmtVal formats a value for display.
func fmtVal(v any) string {
	switch val := v.(type) {
	case string:
		if len(val) > fmtValMaxLen {
			return val[:fmtValMaxLen] + "..."
		}
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case nil:
		return "<nil>"
	default:
		b, _ := json.Marshal(v)
		s := string(b)
		if len(s) > fmtValMaxLen {
			return s[:fmtValMaxLen] + "..."
		}
		return s
	}
}

// sortExtras sorts a slice of KV pairs by key using insertion sort.
func sortExtras(extras []KV) {
	for i := 1; i < len(extras); i++ {
		for j := i; j > 0 && extras[j].Key < extras[j-1].Key; j-- {
			extras[j], extras[j-1] = extras[j-1], extras[j]
		}
	}
}

// strVal extracts a string value from a map, returning "" if the key is
// missing or not a string.
func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
