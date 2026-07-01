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
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// k8sKeyOrder defines conventional ordering for top-level Kubernetes keys.
var k8sKeyOrder = map[string]int{
	"apiVersion": 0,
	"kind":       1,
	"metadata":   2,
	"spec":       3,
	"status":     4,
}

// MarshalYAML serializes a JSON-decoded map to a YAML string using
// Kubernetes-conventional key ordering at the top level and alphabetical
// ordering elsewhere. Only handles types produced by encoding/json.Unmarshal.
func MarshalYAML(obj map[string]any) string {
	var buf strings.Builder
	writeYAMLMap(&buf, obj, 0, true)
	return buf.String()
}

// YAMLLines serializes a JSON-decoded map to YAML and splits into lines.
func YAMLLines(obj map[string]any) []string {
	s := strings.TrimRight(MarshalYAML(obj), "\n")
	return strings.Split(s, "\n")
}

func writeYAMLValue(buf *strings.Builder, v any, indent int) {
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			buf.WriteString(" {}\n")
			return
		}
		buf.WriteString("\n")
		writeYAMLMap(buf, val, indent, false)
	case []any:
		if len(val) == 0 {
			buf.WriteString(" []\n")
			return
		}
		buf.WriteString("\n")
		writeYAMLSlice(buf, val, indent)
	default:
		buf.WriteString(" ")
		writeYAMLScalar(buf, v)
		buf.WriteString("\n")
	}
}

func writeYAMLMap(buf *strings.Builder, m map[string]any, indent int, topLevel bool) {
	keys := sortedMapKeys(m, topLevel)
	prefix := strings.Repeat("  ", indent)
	for _, k := range keys {
		buf.WriteString(prefix)
		buf.WriteString(yamlKey(k))
		buf.WriteString(":")
		writeYAMLValue(buf, m[k], indent+1)
	}
}

func writeYAMLSlice(buf *strings.Builder, s []any, indent int) {
	prefix := strings.Repeat("  ", indent)
	for _, item := range s {
		buf.WriteString(prefix)
		buf.WriteString("- ")
		switch val := item.(type) {
		case map[string]any:
			if len(val) == 0 {
				buf.WriteString("{}\n")
				continue
			}
			keys := sortedMapKeys(val, false)
			buf.WriteString(yamlKey(keys[0]))
			buf.WriteString(":")
			writeYAMLValue(buf, val[keys[0]], indent+2)
			for _, k := range keys[1:] {
				buf.WriteString(prefix)
				buf.WriteString("  ")
				buf.WriteString(yamlKey(k))
				buf.WriteString(":")
				writeYAMLValue(buf, val[k], indent+2)
			}
		case []any:
			buf.WriteString("\n")
			writeYAMLSlice(buf, val, indent+1)
		default:
			writeYAMLScalar(buf, item)
			buf.WriteString("\n")
		}
	}
}

func writeYAMLScalar(buf *strings.Builder, v any) {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case float64:
		if val == float64(int64(val)) {
			buf.WriteString(strconv.FormatInt(int64(val), 10))
		} else {
			buf.WriteString(strconv.FormatFloat(val, 'f', -1, 64))
		}
	case string:
		buf.WriteString(yamlString(val))
	default:
		fmt.Fprintf(buf, "%v", val)
	}
}

// yamlString returns a YAML-safe representation of a string value.
func yamlString(s string) string {
	if s == "" {
		return `""`
	}
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "on", "off", "null", "~":
		return `"` + s + `"`
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return `"` + s + `"`
	}
	needsQuote := false
	for i, c := range s {
		if c == ':' || c == '#' || c == '[' || c == ']' || c == '{' || c == '}' ||
			c == ',' || c == '&' || c == '*' || c == '!' || c == '|' || c == '>' ||
			c == '\'' || c == '"' || c == '%' || c == '@' || c == '`' ||
			c == '\n' || c == '\r' || c == '\t' {
			needsQuote = true
			break
		}
		if i == 0 && (c == '-' || c == '?' || c == ' ') {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return s
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	escaped = strings.ReplaceAll(escaped, "\t", `\t`)
	return `"` + escaped + `"`
}

// yamlKey returns a YAML-safe map key.
func yamlKey(k string) string {
	if k == "" {
		return `""`
	}
	for _, c := range k {
		if c == ':' || c == '#' || c == '[' || c == ']' || c == '{' || c == '}' ||
			c == ',' || c == '&' || c == '*' || c == '!' || c == '|' || c == '>' ||
			c == '\'' || c == '"' || c == '%' || c == '@' || c == '`' || c == ' ' {
			return `"` + strings.ReplaceAll(k, `"`, `\"`) + `"`
		}
	}
	return k
}

func sortedMapKeys(m map[string]any, topLevel bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if topLevel {
			oi, oki := k8sKeyOrder[keys[i]]
			oj, okj := k8sKeyOrder[keys[j]]
			if oki && okj {
				return oi < oj
			}
			if oki {
				return true
			}
			if okj {
				return false
			}
		}
		return keys[i] < keys[j]
	})
	return keys
}
