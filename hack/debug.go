//go:build ignore

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

// debug watches Kubernetes resources via kubectl and prints colored unified
// diffs between object versions, interleaved with filtered controller and
// agent pod logs.
//
// Usage:
//
//	go run hack/debug.go [--log=<path>] [--snapshots=<dir>] <target> [<target> ...]
//
// Where <target> is:
//
//	<kind>            watch all objects of that kind
//	<kind>/<name>     watch a specific named object
//
// Flags:
//
//	--log=<path>        Write a plain-text copy of output to the given file (optional).
//	--snapshots=<dir>   Save full object snapshots to the given directory (optional).
//	                    Files are named <kind>-<name>-<HH-MM-SS.mmm>.yaml and
//	                    object names in the terminal become clickable OSC 8 links.
//
// Examples:
//
//	go run hack/debug.go rsc rsp
//	go run hack/debug.go rsc/my-storage-class rv
//	go run hack/debug.go --log=/tmp/debug.log --snapshots=/tmp/snaps rsc/my-sc rsp
//	KUBECONFIG=~/.kube/my-config go run hack/debug.go rsc
//
// kubectl picks up KUBECONFIG, current context, and all other standard
// environment variables automatically.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// ---------------------------------------------------------------------------
// ANSI colors
// ---------------------------------------------------------------------------

const (
	colorRed       = "\033[31m"
	colorGreen     = "\033[32m"
	colorYellow    = "\033[33m"
	colorCyan      = "\033[36m"
	colorMagenta   = "\033[35m"
	colorDim       = "\033[2m"
	colorBold      = "\033[1m"
	colorBoldRed   = "\033[1;31m"
	colorBoldGreen = "\033[1;32m"
	colorReset     = "\033[0m"
)

var ansiRe = regexp.MustCompile(`\033\[[0-9;]*m`)

// ---------------------------------------------------------------------------
// Serialized output
// ---------------------------------------------------------------------------

var output struct {
	mu  sync.Mutex
	log *os.File
}

// snapshotsDir, when non-empty, enables saving full object snapshots
// and turns object names into clickable OSC 8 terminal hyperlinks.
var snapshotsDir string

func emit(line string) {
	output.mu.Lock()
	defer output.mu.Unlock()

	fmt.Println(line)

	if output.log != nil {
		clean := ansiRe.ReplaceAllString(line, "")
		_, _ = fmt.Fprintln(output.log, clean)
	}
}

// emitBlock outputs multiple lines atomically under a single lock,
// preventing interleaving with output from other goroutines.
func emitBlock(lines []string) {
	if len(lines) == 0 {
		return
	}
	output.mu.Lock()
	defer output.mu.Unlock()

	for _, line := range lines {
		fmt.Println(line)
		if output.log != nil {
			clean := ansiRe.ReplaceAllString(line, "")
			_, _ = fmt.Fprintln(output.log, clean)
		}
	}
}

func ts() string {
	return time.Now().Format("[15:04:05]")
}

// saveSnapshot writes the full (uncleaned) object as pretty-printed JSON to
// <snapshotsDir>/<kind>-<name>-<HH:MM:SS.mmm>.json and returns the absolute
// path. Returns "" if snapshotsDir is empty or the write fails.
func saveSnapshot(kind, name string, obj map[string]any) string {
	if snapshotsDir == "" {
		return ""
	}
	stamp := time.Now().Format("15-04-05.000")
	filename := fmt.Sprintf("%s-%s-%s.yaml", kind, name, stamp)
	p := filepath.Join(snapshotsDir, filename)

	content := marshalYAML(obj)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return ""
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// osc8Link wraps text in an OSC 8 hyperlink escape sequence pointing to a
// local file. If path is empty, text is returned unchanged.
func osc8Link(text, path string) string {
	if path == "" {
		return text
	}
	return fmt.Sprintf("\033]8;;file://%s\033\\%s\033]8;;\033\\", path, text)
}

// ---------------------------------------------------------------------------
// Target parsing
// ---------------------------------------------------------------------------

// target represents a single watch target parsed from CLI arguments.
type target struct {
	kind string // e.g. "rsc", "rsp", "rv"
	name string // empty means "all objects of this kind"
}

func parseTargets(args []string) []target {
	var targets []target
	for _, arg := range args {
		parts := strings.SplitN(arg, "/", 2)
		t := target{kind: parts[0]}
		if len(parts) == 2 {
			t.name = parts[1]
		}
		targets = append(targets, t)
	}
	return targets
}

// watchSet builds lookup structures from targets.
type watchSet struct {
	// kindAll is the set of kinds where all objects are watched.
	kindAll map[string]bool
	// kindNames maps kind -> set of specific names.
	kindNames map[string]map[string]bool
	// allKinds is the deduplicated list of kinds for kubectl watches.
	allKinds []string
}

func buildWatchSet(targets []target) watchSet {
	ws := watchSet{
		kindAll:   make(map[string]bool),
		kindNames: make(map[string]map[string]bool),
	}

	seen := map[string]bool{}
	for _, t := range targets {
		if !seen[t.kind] {
			seen[t.kind] = true
			ws.allKinds = append(ws.allKinds, t.kind)
		}
		if t.name == "" {
			ws.kindAll[t.kind] = true
		} else {
			if ws.kindNames[t.kind] == nil {
				ws.kindNames[t.kind] = map[string]bool{}
			}
			ws.kindNames[t.kind][t.name] = true
		}
	}

	return ws
}

// matchesObject returns true if the given kind/name should be displayed.
func (ws *watchSet) matchesObject(kind, name string) bool {
	if ws.kindAll[kind] {
		return true
	}
	if names, ok := ws.kindNames[kind]; ok {
		return names[name]
	}
	return false
}

// ---------------------------------------------------------------------------
// Controller name mapping
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Kind registry: maps full Kubernetes Kind ↔ kubectl short name.
// Populated dynamically from watch events (obj["kind"]).
// ---------------------------------------------------------------------------

var kindReg struct {
	mu          sync.RWMutex
	shortToFull map[string]string // "rsc" → "ReplicatedStorageClass"
	fullToShort map[string]string // "ReplicatedStorageClass" → "rsc"
}

func init() {
	kindReg.shortToFull = map[string]string{}
	kindReg.fullToShort = map[string]string{}
}

// registerKind records a bidirectional mapping between a kubectl short name
// and the full Kubernetes Kind. Called once per kind when the first watch
// event arrives.
func registerKind(shortKind, fullKind string) {
	kindReg.mu.Lock()
	defer kindReg.mu.Unlock()
	kindReg.shortToFull[shortKind] = fullKind
	kindReg.fullToShort[fullKind] = shortKind
}

// shortKindFor returns the kubectl short name for a full Kubernetes Kind
// (e.g. "ReplicatedStorageClass" → "rsc"). Returns the input unchanged if
// no mapping is registered yet.
func shortKindFor(fullKind string) string {
	kindReg.mu.RLock()
	defer kindReg.mu.RUnlock()
	if s, ok := kindReg.fullToShort[fullKind]; ok {
		return s
	}
	return fullKind
}

// matchesLog returns true if a log entry for the given controllerKind and
// object name matches any of the watched targets.
func (ws *watchSet) matchesLog(controllerKind, name string) bool {
	kind := shortKindFor(controllerKind)
	if ws.kindAll[kind] {
		return true
	}
	if names, ok := ws.kindNames[kind]; ok && names[name] {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Object cleaning
// ---------------------------------------------------------------------------

func cleanObj(obj map[string]any) {
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return
	}
	delete(meta, "managedFields")
	delete(meta, "resourceVersion")
	delete(meta, "uid")
	delete(meta, "creationTimestamp")

	ann, _ := meta["annotations"].(map[string]any)
	delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
	if len(ann) == 0 {
		delete(meta, "annotations")
	}
}

func prettyLines(obj map[string]any) []string {
	cleanObj(obj)
	return yamlLines(obj)
}

// ---------------------------------------------------------------------------
// Minimal YAML marshaler (stdlib-only, handles types from json.Unmarshal)
// ---------------------------------------------------------------------------

// k8sKeyOrder defines conventional ordering for top-level Kubernetes keys.
var k8sKeyOrder = map[string]int{
	"apiVersion": 0,
	"kind":       1,
	"metadata":   2,
	"spec":       3,
	"status":     4,
}

func marshalYAML(obj map[string]any) string {
	var buf strings.Builder
	writeYAMLMap(&buf, obj, 0, true)
	return buf.String()
}

func yamlLines(obj map[string]any) []string {
	s := strings.TrimRight(marshalYAML(obj), "\n")
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
			// First key goes on the "- " line.
			buf.WriteString(yamlKey(keys[0]))
			buf.WriteString(":")
			writeYAMLValue(buf, val[keys[0]], indent+2)
			// Remaining keys indented to align with the first key.
			for _, k := range keys[1:] {
				buf.WriteString(prefix)
				buf.WriteString("  ")
				buf.WriteString(yamlKey(k))
				buf.WriteString(":")
				writeYAMLValue(buf, val[k], indent+2)
			}
		case []any:
			// Nested list item — rare but handle it.
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
	// Values that YAML would interpret as non-string.
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

// ---------------------------------------------------------------------------
// Conditions table
// ---------------------------------------------------------------------------

type condition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}

// extractConditions reads .status.conditions from an object.
func extractConditions(obj map[string]any) []condition {
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		return nil
	}
	raw, _ := status["conditions"].([]any)
	if len(raw) == 0 {
		return nil
	}
	conds := make([]condition, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		conds = append(conds, condition{
			Type:    strVal(m, "type"),
			Status:  strVal(m, "status"),
			Reason:  strVal(m, "reason"),
			Message: strVal(m, "message"),
		})
	}
	return conds
}

// removeConditions removes .status.conditions from the object so they don't
// appear in the unified JSON diff (they're rendered separately as a table).
func removeConditions(obj map[string]any) {
	status, _ := obj["status"].(map[string]any)
	if status == nil {
		return
	}
	delete(status, "conditions")
}

// statusIcon returns a colored icon for a condition status.
func statusIcon(status string) string {
	switch status {
	case "True":
		return fmt.Sprintf("%s✓%s", colorGreen, colorReset)
	case "False":
		return fmt.Sprintf("%s✗%s", colorRed, colorReset)
	default: // Unknown
		return fmt.Sprintf("%s?%s", colorYellow, colorReset)
	}
}

// statusIconDim returns a dim status icon (for unchanged conditions).
func statusIconDim(status string) string {
	switch status {
	case "True":
		return "✓"
	case "False":
		return "✗"
	default:
		return "?"
	}
}

// statusColored returns the status string with color.
// Trims spaces before matching, preserves original width with padding after color.
func statusColored(status string) string {
	trimmed := strings.TrimSpace(status)
	var colored string
	switch trimmed {
	case "True":
		colored = fmt.Sprintf("%sTrue%s", colorGreen, colorReset)
	case "False":
		colored = fmt.Sprintf("%sFalse%s", colorRed, colorReset)
	default:
		colored = fmt.Sprintf("%s%s%s", colorYellow, trimmed, colorReset)
	}
	// Preserve original padding (ANSI codes are zero-width).
	if extra := len(status) - len(trimmed); extra > 0 {
		colored += strings.Repeat(" ", extra)
	}
	return colored
}

// termWidth returns the current terminal column count, or 200 as a fallback.
func termWidth() int {
	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	var ws winsize
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdout),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)))
	if err != 0 || ws.Col == 0 {
		return 200
	}
	return int(ws.Col)
}

func truncMsg(msg string, maxW int) string {
	if maxW <= 0 {
		maxW = 200
	}
	if len(msg) > maxW {
		if maxW > 3 {
			return msg[:maxW-3] + "..."
		}
		return msg[:maxW]
	}
	return msg
}

// pad right-pads s with spaces to width (plain text, no ANSI).
func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// condWidths computes column widths from actual condition data.
func condWidths(sets ...[]condition) (typeW, statusW, reasonW int) {
	for _, cs := range sets {
		for _, c := range cs {
			if len(c.Type) > typeW {
				typeW = len(c.Type)
			}
			if len(c.Status) > statusW {
				statusW = len(c.Status)
			}
			if len(c.Reason) > reasonW {
				reasonW = len(c.Reason)
			}
		}
	}
	return
}

// condMsgMaxWidth returns the maximum message width given column widths.
// Prefix layout: "  │ X ✓ " (8 display chars) + tw + " " + sw + " " + rw + " ".
func condMsgMaxWidth(tw, sw, rw int) int {
	prefix := 8 + tw + 1 + sw + 1 + rw + 1 // +1 for each inter-column space
	w := termWidth() - prefix
	if w < 20 {
		w = 20
	}
	return w
}

// conditionsTableNew returns formatted lines for a conditions table of a newly
// ADDED object. Does NOT include closing └ — caller handles border transitions.
func conditionsTableNew(oldConds, newConds []condition) []string {
	tw, sw, rw := condWidths(newConds)
	mw := condMsgMaxWidth(tw, sw, rw)
	lines := []string{fmt.Sprintf("  %s┌ conditions%s", colorDim, colorReset)}
	for _, c := range newConds {
		lines = append(lines, fmt.Sprintf("  %s│%s %s %s %s %s %s %s%s%s",
			colorDim, colorReset,
			colorGreen+"+"+colorReset,
			statusIcon(c.Status),
			pad(c.Type, tw),
			statusColored(pad(c.Status, sw)),
			pad(c.Reason, rw),
			colorDim, truncMsg(c.Message, mw), colorReset,
		))
	}
	return lines
}

// conditionsTableDiff returns formatted lines showing the conditions diff.
// Returns nil only when there are no conditions at all and nothing changed.
// Does NOT include closing └ — caller handles border transitions.
func conditionsTableDiff(oldConds, newConds []condition) []string {
	// Build maps by type.
	oldMap := make(map[string]condition, len(oldConds))
	for _, c := range oldConds {
		oldMap[c.Type] = c
	}
	newMap := make(map[string]condition, len(newConds))
	for _, c := range newConds {
		newMap[c.Type] = c
	}

	changed := !conditionsEqual(oldConds, newConds)

	// If nothing changed and there are no conditions at all, skip the table.
	if !changed && len(newConds) == 0 {
		return nil
	}

	tw, sw, rw := condWidths(oldConds, newConds)

	// Expand column widths to fit transition strings (e.g. "Unknown→True").
	for _, c := range newConds {
		old, existed := oldMap[c.Type]
		if !existed {
			continue
		}
		if old.Status != c.Status {
			if w := len(old.Status) + 1 + len(c.Status); w > sw {
				sw = w
			}
		}
		if old.Reason != c.Reason {
			if w := len(old.Reason) + 1 + len(c.Reason); w > rw {
				rw = w
			}
		}
	}

	mw := condMsgMaxWidth(tw, sw, rw)

	var lines []string

	// When nothing changed, render the whole table in dim but keep status colored.
	if !changed {
		lines = append(lines, fmt.Sprintf("  %s┌ conditions (unchanged)%s", colorDim, colorReset))
		for _, c := range newConds {
			lines = append(lines, fmt.Sprintf("  %s│   %s %s %s %s %s%s",
				colorDim,
				statusIconDim(c.Status),
				pad(c.Type, tw),
				statusColored(pad(c.Status, sw)),
				pad(c.Reason, rw),
				truncMsg(c.Message, mw), colorReset,
			))
		}
		return lines
	}

	lines = append(lines, fmt.Sprintf("  %s┌ conditions%s", colorDim, colorReset))

	// Show new conditions in their order, marking changes.
	for _, c := range newConds {
		old, existed := oldMap[c.Type]
		if !existed {
			// Added condition.
			lines = append(lines, fmt.Sprintf("  %s│%s %s %s %s %s %s %s%s%s",
				colorDim, colorReset,
				colorGreen+"+"+colorReset,
				statusIcon(c.Status),
				pad(c.Type, tw),
				statusColored(pad(c.Status, sw)),
				pad(c.Reason, rw),
				colorDim, truncMsg(c.Message, mw), colorReset,
			))
		} else if old.Status != c.Status || old.Reason != c.Reason || old.Message != c.Message {
			// Changed condition.
			statusStr := statusColored(pad(c.Status, sw))
			if old.Status != c.Status {
				plainW := len(old.Status) + 1 + len(c.Status)
				statusStr = fmt.Sprintf("%s→%s", statusColored(old.Status), statusColored(c.Status))
				if plainW < sw {
					statusStr += strings.Repeat(" ", sw-plainW)
				}
			}
			reasonStr := pad(c.Reason, rw)
			if old.Reason != c.Reason {
				plainW := len(old.Reason) + 1 + len(c.Reason)
				reasonStr = fmt.Sprintf("%s%s%s→%s", colorDim, old.Reason, colorReset, c.Reason)
				if plainW < rw {
					reasonStr += strings.Repeat(" ", rw-plainW)
				}
			}
			lines = append(lines, fmt.Sprintf("  %s│%s %s %s %s %s %s %s%s%s",
				colorDim, colorReset,
				colorYellow+"~"+colorReset,
				statusIcon(c.Status),
				pad(c.Type, tw),
				statusStr,
				reasonStr,
				colorDim, truncMsg(c.Message, mw), colorReset,
			))
		} else {
			// Unchanged condition — show dim for context but keep status colored.
			lines = append(lines, fmt.Sprintf("  %s│   %s %s%s %s %s%s %s%s",
				colorDim,
				statusIcon(c.Status),
				colorDim, pad(c.Type, tw),
				statusColored(pad(c.Status, sw)),
				colorDim, pad(c.Reason, rw),
				truncMsg(c.Message, mw), colorReset,
			))
		}
	}

	// Show removed conditions.
	for _, c := range oldConds {
		if _, exists := newMap[c.Type]; !exists {
			lines = append(lines, fmt.Sprintf("  %s│%s %s %s %s%s (removed)%s",
				colorDim, colorReset,
				colorRed+"-"+colorReset,
				statusIcon(c.Status),
				colorRed, pad(c.Type, tw), colorReset,
			))
		}
	}

	return lines
}

func conditionsEqual(a, b []condition) bool {
	if len(a) != len(b) {
		return false
	}
	am := make(map[string]condition, len(a))
	for _, c := range a {
		am[c.Type] = c
	}
	for _, c := range b {
		old, ok := am[c.Type]
		if !ok {
			return false
		}
		if old.Status != c.Status || old.Reason != c.Reason || old.Message != c.Message {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Resource watcher
// ---------------------------------------------------------------------------

// watchResource spawns "kubectl get <kind> [<name>] -w --output-watch-events -o json"
// and feeds events into the shared display pipeline.
func watchResource(ctx context.Context, kind string, names []string, ws *watchSet, wg *sync.WaitGroup) {
	defer wg.Done()

	// If we have specific names and the kind is NOT also watched broadly,
	// start one watch per name. Otherwise watch the whole kind.
	if len(names) > 0 && !ws.kindAll[kind] {
		var innerWg sync.WaitGroup
		for _, n := range names {
			innerWg.Add(1)
			go watchSingleResource(ctx, kind, n, ws, &innerWg)
		}
		innerWg.Wait()
		return
	}

	watchSingleResource(ctx, kind, "", ws, nil)
}

func watchSingleResource(ctx context.Context, kind, name string, ws *watchSet, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	label := kind
	if name != "" {
		label = kind + "/" + name
	}

	// objState stores the previous cleaned object (without conditions) and conditions.
	type objState struct {
		lines      []string
		conditions []condition
	}
	// State persists across reconnects so we don't re-emit ADDED for known objects.
	state := map[string]objState{}

	for {
		if ctx.Err() != nil {
			return
		}

		args := []string{"get", kind}
		if name != "" {
			args = append(args, name)
		}
		args = append(args, "-w", "--output-watch-events", "-o", "json")

		cmd := exec.CommandContext(ctx, "kubectl", args...)
		cmd.Stderr = os.Stderr

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			emit(fmt.Sprintf("%s %s[%s]%s failed to create pipe: %v", ts(), colorCyan, label, colorReset, err))
			sleepCtx(ctx, 2*time.Second)
			continue
		}

		if err := cmd.Start(); err != nil {
			emit(fmt.Sprintf("%s %s[%s]%s failed to start kubectl: %v", ts(), colorCyan, label, colorReset, err))
			sleepCtx(ctx, 2*time.Second)
			continue
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 4*1024*1024), 4*1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var event map[string]any
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}

			eventType, _ := event["type"].(string)
			obj, _ := event["object"].(map[string]any)
			if obj == nil {
				continue
			}

			// Register the full Kind → short kind mapping on first event.
			if fullKind, _ := obj["kind"].(string); fullKind != "" {
				registerKind(kind, fullKind)
			}

			meta, _ := obj["metadata"].(map[string]any)
			objName, _ := meta["name"].(string)
			if objName == "" {
				objName = "?"
			}

			// Filter: only show objects that match the watch set.
			if !ws.matchesObject(kind, objName) {
				continue
			}

			// Save full snapshot before any mutations (if snapshots enabled).
			snapPath := saveSnapshot(kind, objName, obj)
			objLink := objName
			if snapPath != "" {
				objLink = osc8Link(objName, snapPath)
			}

			// Extract conditions before cleaning for diff.
			newConds := extractConditions(obj)
			// Remove conditions from obj so they don't appear in the JSON diff.
			removeConditions(obj)

			newLines := prettyLines(obj)

			switch eventType {
			case "DELETED":
				emit(fmt.Sprintf("%s %s[%s]%s %sDELETED%s %s",
					ts(), colorCyan, kind, colorReset, colorRed, colorReset, objLink))
				delete(state, objName)

			case "BOOKMARK":
				// Bookmark events are informational; skip them to avoid corrupting state.
				continue

			default: // ADDED, MODIFIED
				prev, exists := state[objName]
				state[objName] = objState{lines: newLines, conditions: newConds}

				// Collect all output lines into a block for atomic emission.
				var block []string

				if !exists {
					block = append(block, fmt.Sprintf("%s %s[%s]%s %sADDED%s %s",
						ts(), colorCyan, kind, colorReset, colorGreen, colorReset, objLink))

					hasConds := len(newConds) > 0
					if hasConds {
						block = append(block, conditionsTableNew(nil, newConds)...)
						block = append(block, fmt.Sprintf("  %s├──%s", colorDim, colorReset))
					} else {
						block = append(block, fmt.Sprintf("  %s┌%s", colorDim, colorReset))
					}
					for _, l := range newLines {
						block = append(block, fmt.Sprintf("  %s│%s %s%s%s", colorDim, colorReset, colorDim, l, colorReset))
					}
					block = append(block, fmt.Sprintf("  %s└%s", colorDim, colorReset))
					emitBlock(block)
					continue
				}

				// Compute diff.
				diff := unifiedDiff(prev.lines, newLines)
				condsChanged := !conditionsEqual(prev.conditions, newConds)

				if len(diff) == 0 && !condsChanged {
					continue
				}

				block = append(block, fmt.Sprintf("%s %s[%s]%s MODIFIED %s",
					ts(), colorCyan, kind, colorReset, objLink))

				// Always show conditions table on modification.
				condLines := conditionsTableDiff(prev.conditions, newConds)
				hasConds := len(condLines) > 0
				hasDiff := len(diff) > 0
				block = append(block, condLines...)

				if hasDiff {
					if hasConds {
						block = append(block, fmt.Sprintf("  %s├──%s", colorDim, colorReset))
					} else {
						block = append(block, fmt.Sprintf("  %s┌%s", colorDim, colorReset))
					}
					bar := fmt.Sprintf("  %s│%s ", colorDim, colorReset)
					for _, d := range diff {
						switch {
						case strings.HasPrefix(d, "+"):
							block = append(block, fmt.Sprintf("%s%s%s%s", bar, colorGreen, d, colorReset))
						case strings.HasPrefix(d, "-"):
							block = append(block, fmt.Sprintf("%s%s%s%s", bar, colorRed, d, colorReset))
						case strings.HasPrefix(d, "@@") || strings.HasPrefix(d, "──"):
							block = append(block, fmt.Sprintf("%s%s%s%s", bar, colorCyan, d, colorReset))
						default:
							block = append(block, fmt.Sprintf("%s%s", bar, d))
						}
					}
				}
				block = append(block, fmt.Sprintf("  %s└%s", colorDim, colorReset))
				emitBlock(block)
			}
		}

		_ = cmd.Wait()

		// kubectl watch disconnected (API server timeout, network issue, etc.) — reconnect.
		if ctx.Err() != nil {
			return
		}
		emit(fmt.Sprintf("%s %s[%s]%s watch disconnected, reconnecting...", ts(), colorDim, label, colorReset))
		sleepCtx(ctx, 1*time.Second)
	}
}

// ---------------------------------------------------------------------------
// Pod log streaming with restart handling
// ---------------------------------------------------------------------------

const podNamespace = "d8-sds-replicated-volume"

// followPodLogs continuously tails logs from pods matching the given label
// selector. On disconnect (pod restart, rollover), it reconnects after a
// brief pause. Uses exponential backoff when no log lines are received
// (e.g. pods are not running). Runs until ctx is cancelled.
func followPodLogs(ctx context.Context, component, labelSelector string, ws *watchSet, wg *sync.WaitGroup) {
	defer wg.Done()

	// Track the last reconcileID per (controller, name) for separator drawing.
	lastReconcileID := map[string]string{} // "controller\x00name" -> reconcileID

	const (
		backoffMin = 2 * time.Second
		backoffMax = 30 * time.Second
	)
	backoff := backoffMin
	notifiedWaiting := false  // true after we printed "waiting for pods"
	var lastLogTime time.Time // timestamp of the last received log entry

	for {
		if ctx.Err() != nil {
			return
		}

		// On first connect use --since=1s (only recent logs).
		// On reconnect compute the gap since the last received log entry
		// so we don't miss startup logs after a pod restart.
		sinceArg := "--since=1s"
		if !lastLogTime.IsZero() {
			gapSec := int(time.Since(lastLogTime).Seconds()) + 5 // +5s buffer for clock skew
			if gapSec < 1 {
				gapSec = 1
			}
			sinceArg = fmt.Sprintf("--since=%ds", gapSec)
		}

		args := []string{
			"logs", "-f",
			"-l", labelSelector,
			"-n", podNamespace,
			"--all-containers",
			"--prefix",
			sinceArg,
			"--max-log-requests=50",
		}

		cmd := exec.CommandContext(ctx, "kubectl", args...)
		// Capture stderr so that kubectl "error: ... no matching pods" does not
		// spam the terminal when pods are absent.
		var stderrBuf strings.Builder
		cmd.Stderr = &stderrBuf

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			emit(fmt.Sprintf("%s %s[%s]%s failed to create pipe: %v", ts(), colorMagenta, component, colorReset, err))
			sleepCtx(ctx, backoff)
			continue
		}

		if err := cmd.Start(); err != nil {
			emit(fmt.Sprintf("%s %s[%s]%s failed to start kubectl logs: %v", ts(), colorMagenta, component, colorReset, err))
			sleepCtx(ctx, backoff)
			continue
		}

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1*1024*1024), 1*1024*1024)

		gotLines := false
		for scanner.Scan() {
			raw := scanner.Text()
			if raw == "" {
				continue
			}
			gotLines = true
			lastLogTime = time.Now()

			// kubectl --prefix prepends "[pod/container] " to each line.
			jsonStr := raw
			if idx := strings.Index(raw, "] "); idx >= 0 {
				jsonStr = raw[idx+2:]
			}

			entry := parseLogEntry(jsonStr)
			if entry == nil {
				// Non-JSON line (e.g. startup noise) — show as-is if it looks useful.
				if len(jsonStr) > 0 {
					emit(fmt.Sprintf("%s %s[%s]%s %s", ts(), badgeColor(component), component, colorReset, jsonStr))
				}
				continue
			}

			if !filterLogEntry(entry, ws) {
				continue
			}

			formatted := formatLogEntry(entry, component, lastReconcileID)
			emitBlock(formatted)
		}

		_ = cmd.Wait()

		if ctx.Err() != nil {
			return
		}

		if gotLines {
			// Had real output → normal reconnect with minimal backoff.
			backoff = backoffMin
			notifiedWaiting = false
			emit(fmt.Sprintf("%s %s[%s]%s %s── reconnecting ──%s",
				ts(), badgeColor(component), component, colorReset, colorDim, colorReset))
		} else {
			// No output at all → pods are likely absent. Print once, then back off silently.
			if !notifiedWaiting {
				emit(fmt.Sprintf("%s %s[%s]%s %s── no pods, waiting ──%s",
					ts(), badgeColor(component), component, colorReset, colorDim, colorReset))
				notifiedWaiting = true
			}
			// Exponential backoff.
			backoff = backoff * 2
			if backoff > backoffMax {
				backoff = backoffMax
			}
		}
		sleepCtx(ctx, backoff)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func badgeColor(component string) string {
	switch component {
	case "controller":
		return colorMagenta
	case "agent":
		return colorYellow
	default:
		return colorCyan
	}
}

// ---------------------------------------------------------------------------
// JSON log parsing
// ---------------------------------------------------------------------------

type logEntry struct {
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
	PhasePath []string // segments of the "logger" field
	PhaseName string   // last segment of PhasePath
	Result    string
	Changed   string
	HasError  string
	Duration  string

	// Extra key-value pairs not covered by dedicated fields, for display.
	extras []kv

	// All raw fields for fallback display.
	raw map[string]any
}

type kv struct {
	key  string
	val  string // display value (may be a brief summary)
	file string // optional: path to saved file (for clickable link)
}

// knownKeys are the standard fields that we extract into dedicated logEntry fields.
// Everything else goes into extras for display.
var knownKeys = map[string]bool{
	"time": true, "level": true, "msg": true,
	"controller": true, "controllerGroup": true, "controllerKind": true,
	"namespace": true, "name": true, "reconcileID": true,
	"startedAt": true,
	"err":       true, "error": true,
	// Phase data: logr→slog "logger" field + flow.*.OnEnd attrs.
	"logger": true,
	"result": true, "changed": true, "hasError": true, "duration": true,
	"requeueAfter": true, "phaseName": true,
}

func parseLogEntry(line string) *logEntry {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return nil
	}

	e := &logEntry{raw: m}
	e.Time = strVal(m, "time")
	e.Level = strVal(m, "level")
	e.Msg = strVal(m, "msg")
	e.Controller = strVal(m, "controller")
	e.Kind = strVal(m, "controllerKind")
	e.Name = strVal(m, "name")
	e.ReconcileID = strVal(m, "reconcileID")

	// Error field: can be top-level "err" or "error".
	e.Error = strVal(m, "err")
	if e.Error == "" {
		e.Error = strVal(m, "error")
	}

	// Phase path: the logr→slog adapter stores the WithName chain as a flat
	// "logger" field (e.g. "schedule-rvr/place-rvr/rvr-condition").
	if logger := strVal(m, "logger"); logger != "" {
		e.PhasePath = strings.Split(logger, "/")
		e.PhaseName = e.PhasePath[len(e.PhasePath)-1]
	}

	// Phase-end metadata (flat top-level fields set by flow.*.OnEnd).
	e.Result = strVal(m, "result")
	e.Changed = strVal(m, "changed")
	e.HasError = strVal(m, "hasError")
	e.Duration = strVal(m, "duration")

	// Collect extra fields not covered by dedicated struct fields.
	for k, v := range m {
		if knownKeys[k] {
			continue
		}
		// Skip the primary reconciled object — its kind/name is already in the
		// log header (e.g. "rvr/test-rv-100mi-r1-0"), so showing
		// ReplicatedVolumeReplica={"name":"..."} on every line is pure noise.
		if k == e.Kind {
			continue
		}
		// "source" appears twice in controller-runtime JSON: once as a slog
		// AddSource object ({"function":...,"file":...,"line":...}) and once
		// as a string from the controller event source (e.g. "kind source:
		// *v1alpha1.ReplicatedStorageClass"). json.Unmarshal keeps the last
		// value, so we see the string for Starting EventSource messages and
		// the object for everything else. Skip the object, show the string.
		if k == "source" {
			if s, ok := v.(string); ok {
				s = strings.TrimPrefix(s, "kind source: ")
				e.extras = append(e.extras, kv{key: "source", val: s})
			}
			continue
		}
		e.extras = append(e.extras, processExtraValue(k, v))
	}
	sortExtras(e.extras)

	return e
}

// ---------------------------------------------------------------------------
// Hexdump parsing and Kubernetes protobuf metadata extraction
// ---------------------------------------------------------------------------

// isHexDump reports whether s looks like the output of encoding/hex.Dump
// (e.g. "00000000  6b 38 73 00 ...  |k8s...|").
func isHexDump(s string) bool {
	if len(s) < 10 {
		return false
	}
	for i := 0; i < 8; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return s[8] == ' ' && s[9] == ' '
}

// parseHexDump extracts raw bytes from encoding/hex.Dump formatted output.
func parseHexDump(s string) []byte {
	var result []byte
	for _, line := range strings.Split(s, "\n") {
		if len(line) < 10 || line[8] != ' ' || line[9] != ' ' {
			continue
		}
		hexPart := line[10:]
		if pipe := strings.IndexByte(hexPart, '|'); pipe >= 0 {
			hexPart = hexPart[:pipe]
		}
		for _, token := range strings.Fields(hexPart) {
			if len(token) != 2 {
				continue
			}
			b, err := strconv.ParseUint(token, 16, 8)
			if err != nil {
				continue
			}
			result = append(result, byte(b))
		}
	}
	return result
}

// k8sProtobufMagic is the 4-byte magic header for Kubernetes protobuf encoding.
const k8sProtobufMagic = "k8s\x00"

// extractK8sProtobufMeta extracts apiVersion and kind from Kubernetes
// protobuf-encoded bytes. The wire format is:
//
//	magic("k8s\0") + runtime.Unknown{ field1: TypeMeta{ field1: apiVersion, field2: kind }, field2: Raw, ... }
func extractK8sProtobufMeta(data []byte) (apiVersion, kind string) {
	if len(data) < 4 || string(data[:4]) != k8sProtobufMagic {
		return "", ""
	}
	data = data[4:]

	// Parse runtime.Unknown: look for field 1 (TypeMeta, length-delimited).
	for len(data) > 0 {
		tag, n := protoDecodeVarint(data)
		if n == 0 {
			return
		}
		data = data[n:]
		fieldNum := tag >> 3
		wireType := tag & 0x7

		switch wireType {
		case 2: // length-delimited
			length, ln := protoDecodeVarint(data)
			if ln == 0 || int(length) > len(data[ln:]) {
				return
			}
			data = data[ln:]
			if fieldNum == 1 {
				return parseProtoTypeMeta(data[:length])
			}
			data = data[length:]
		case 0: // varint
			_, vn := protoDecodeVarint(data)
			if vn == 0 {
				return
			}
			data = data[vn:]
		case 1: // 64-bit
			if len(data) < 8 {
				return
			}
			data = data[8:]
		case 5: // 32-bit
			if len(data) < 4 {
				return
			}
			data = data[4:]
		default:
			return
		}
	}
	return
}

// parseProtoTypeMeta decodes the TypeMeta protobuf message:
// field 1 = apiVersion (string), field 2 = kind (string).
func parseProtoTypeMeta(data []byte) (apiVersion, kind string) {
	for len(data) > 0 {
		tag, n := protoDecodeVarint(data)
		if n == 0 {
			return
		}
		data = data[n:]
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType != 2 {
			// Skip non-length-delimited fields.
			switch wireType {
			case 0:
				_, vn := protoDecodeVarint(data)
				if vn == 0 {
					return
				}
				data = data[vn:]
			case 1:
				if len(data) < 8 {
					return
				}
				data = data[8:]
			case 5:
				if len(data) < 4 {
					return
				}
				data = data[4:]
			default:
				return
			}
			continue
		}

		length, ln := protoDecodeVarint(data)
		if ln == 0 || int(length) > len(data[ln:]) {
			return
		}
		data = data[ln:]
		switch fieldNum {
		case 1:
			apiVersion = string(data[:length])
		case 2:
			kind = string(data[:length])
		}
		data = data[length:]
	}
	return
}

// protoDecodeVarint decodes a protobuf varint from the beginning of data.
// Returns the value and the number of bytes consumed (0 if truncated).
func protoDecodeVarint(data []byte) (uint64, int) {
	var value uint64
	for i, b := range data {
		if i >= 10 {
			return 0, 0
		}
		value |= uint64(b&0x7f) << (7 * uint(i))
		if b&0x80 == 0 {
			return value, i + 1
		}
	}
	return 0, 0
}

// processHexDumpValue handles a hexdump string (typically a protobuf response body):
// parses the hex, tries to extract Kubernetes TypeMeta, saves to file, returns brief summary.
func processHexDumpValue(key, hexStr string) kv {
	raw := parseHexDump(hexStr)

	var brief string
	if len(raw) >= 4 && string(raw[:4]) == k8sProtobufMagic {
		apiVersion, k := extractK8sProtobufMeta(raw)
		sk := shortKindFor(k)
		switch {
		case k != "" && apiVersion != "":
			brief = fmt.Sprintf("pb %s (%s) %d bytes", sk, apiVersion, len(raw))
		case k != "":
			brief = fmt.Sprintf("pb %s %d bytes", sk, len(raw))
		default:
			brief = fmt.Sprintf("pb %d bytes", len(raw))
		}
	} else if len(raw) > 0 {
		brief = fmt.Sprintf("binary %d bytes", len(raw))
	} else {
		brief = fmt.Sprintf("hexdump %d chars", len(hexStr))
	}

	path := saveLogBody(key, []byte(hexStr))
	return kv{key: key, val: brief, file: path}
}

// longValueThreshold is the character count above which a value is considered
// "long" and should be saved to a file instead of displayed inline.
const longValueThreshold = 120

// processExtraValue converts a raw JSON field into a kv for display.
// Long values and Kubernetes-like objects are saved to files with brief summaries.
func processExtraValue(key string, raw any) kv {
	switch val := raw.(type) {
	case map[string]any:
		return processMapValue(key, val)
	case []any:
		b, _ := json.Marshal(val)
		s := string(b)
		if len(s) <= longValueThreshold {
			return kv{key: key, val: s}
		}
		path := saveLogBody(key, b)
		return kv{key: key, val: fmt.Sprintf("[%d items]", len(val)), file: path}
	case string:
		// Hexdump (protobuf response bodies from client-go V8 transport logging).
		if isHexDump(val) {
			return processHexDumpValue(key, val)
		}
		// Try to parse string as JSON object (some libraries serialize objects to string).
		if len(val) > longValueThreshold && len(val) > 2 && val[0] == '{' {
			var obj map[string]any
			if json.Unmarshal([]byte(val), &obj) == nil {
				return processMapValue(key, obj)
			}
		}
		if len(val) <= longValueThreshold {
			return kv{key: key, val: val}
		}
		path := saveLogBody(key, []byte(val))
		return kv{key: key, val: val[:longValueThreshold] + "…", file: path}
	default:
		return kv{key: key, val: fmtVal(raw)}
	}
}

// processMapValue handles a JSON object: tries to extract Kubernetes resource
// metadata for a brief summary. Only saves to file if the serialized JSON
// exceeds longValueThreshold; small objects are shown inline as compact JSON.
func processMapValue(key string, obj map[string]any) kv {
	// Kubernetes-like object → brief summary (e.g. "rvr/name rv=123").
	if brief := briefKubeResource(obj); brief != "" {
		b, _ := json.MarshalIndent(obj, "", "  ")
		var path string
		if len(b) > longValueThreshold {
			path = saveLogBody(key, b)
		}
		return kv{key: key, val: brief, file: path}
	}

	// Not a Kubernetes object → compact JSON inline or save to file.
	compact, _ := json.Marshal(obj)
	s := string(compact)
	if len(s) <= longValueThreshold {
		return kv{key: key, val: s}
	}
	pretty, _ := json.MarshalIndent(obj, "", "  ")
	path := saveLogBody(key, pretty)
	return kv{key: key, val: fmt.Sprintf("{%d fields}", len(obj)), file: path}
}

// briefKubeResource returns a brief description of a Kubernetes-like JSON
// object: "Kind/name rv=123" or "Kind/ns/name/status rv=123".
// Returns "" if the object doesn't look like a Kubernetes resource.
func briefKubeResource(obj map[string]any) string {
	kind, _ := obj["kind"].(string)
	if kind == "" {
		return ""
	}
	meta, _ := obj["metadata"].(map[string]any)
	if meta == nil {
		return ""
	}

	// Kubernetes API error responses (kind: "Status") have a different shape:
	// no real metadata, but have "reason", "code", "message", "details" fields.
	if kind == "Status" {
		reason, _ := obj["reason"].(string)
		code, _ := obj["code"].(float64)

		// Extract the target object from "details" if present.
		var target string
		if details, ok := obj["details"].(map[string]any); ok {
			dKind, _ := details["kind"].(string)
			dName, _ := details["name"].(string)
			if dKind != "" && dName != "" {
				target = shortKindFor(dKind) + "/" + dName
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

	// Use short kind if registered.
	displayKind := shortKindFor(kind)

	// Build "Kind/[ns/]name" or "Kind/[ns/]name/status".
	var ident string
	if ns != "" {
		ident = fmt.Sprintf("%s/%s/%s", displayKind, ns, name)
	} else if name != "" {
		ident = fmt.Sprintf("%s/%s", displayKind, name)
	} else {
		ident = displayKind
	}

	// Detect status subresource payload: has "status" as a map but no "spec".
	// (The "status" field must be a map — a plain string like "Failure" in
	// Status error responses is not a subresource.)
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

// saveLogBody writes data to a timestamped file in the snapshots directory.
// Returns the absolute path, or "" if snapshots are disabled or write fails.
func saveLogBody(key string, data []byte) string {
	if snapshotsDir == "" {
		return ""
	}
	stamp := time.Now().Format("15-04-05.000")
	filename := fmt.Sprintf("log-%s-%s.json", stamp, key)
	p := filepath.Join(snapshotsDir, filename)

	if err := os.WriteFile(p, data, 0o644); err != nil {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// fmtVal formats a value for display.
func fmtVal(v any) string {
	switch val := v.(type) {
	case string:
		if len(val) > 200 {
			return val[:200] + "..."
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
		if len(s) > 200 {
			return s[:200] + "..."
		}
		return s
	}
}

func sortExtras(extras []kv) {
	for i := 1; i < len(extras); i++ {
		for j := i; j > 0 && extras[j].key < extras[j-1].key; j-- {
			extras[j], extras[j-1] = extras[j-1], extras[j]
		}
	}
}

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// ---------------------------------------------------------------------------
// Log filtering
// ---------------------------------------------------------------------------

func filterLogEntry(e *logEntry, ws *watchSet) bool {
	// Skip Request/Response Body messages with empty body (GET/LIST/WATCH requests).
	if e.Msg == "Request Body" || e.Msg == "Response Body" {
		body, exists := e.raw["body"]
		if !exists || body == nil || body == "" {
			return false
		}
	}

	// Has controllerKind + name → reconcile log for a specific object.
	// Only show if the object matches the watch set.
	if e.Kind != "" && e.Name != "" {
		if ws.matchesLog(e.Kind, e.Name) {
			return true
		}
		// Fallback: match by controller name prefix.
		// Example: rvr-scheduling-controller starts with "rvr-" → matches "rvr" target.
		// This covers controllers that reconcile a different primary kind (e.g. RV)
		// but are logically associated with a watched kind (e.g. RVR).
		if e.Controller != "" {
			return ws.matchesControllerName(e.Controller)
		}
		return false
	}

	// No specific object name → controller-level lifecycle message or global
	// message (Starting EventSource, Starting Controller, Starting workers,
	// building controller, shutdown, etc.). Always show regardless of watch
	// set — these are informational and help understand system state.
	return true
}

// matchesControllerName returns true if the controller name starts with any
// watched kind prefix (e.g. controller "rvr-scheduling-controller" matches
// watched kind "rvr" because it starts with "rvr-").
func (ws *watchSet) matchesControllerName(controllerName string) bool {
	for _, kind := range ws.allKinds {
		if strings.HasPrefix(controllerName, kind+"-") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Log visualization
// ---------------------------------------------------------------------------

func formatLogEntry(e *logEntry, component string, lastReconcileID map[string]string) []string {
	var lines []string

	badge := fmt.Sprintf("%s[%s]%s", badgeColor(component), component, colorReset)

	// Short reconcile ID (first 8 chars).
	shortID := ""
	if e.ReconcileID != "" {
		shortID = e.ReconcileID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
	}

	// Draw reconcile boundary separator when reconcileID changes.
	if e.Controller != "" && e.Name != "" && e.ReconcileID != "" {
		key := e.Controller + "\x00" + e.Name
		prev, hasPrev := lastReconcileID[key]
		lastReconcileID[key] = e.ReconcileID
		if hasPrev && prev != e.ReconcileID {
			lines = append(lines, fmt.Sprintf(
				"%s────────── reconcile %s done ──────────%s",
				colorDim, shortReconcileID(prev), colorReset))
		}
	}

	// Timestamp.
	timeStr := ""
	if e.Time != "" {
		if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
			timeStr = t.Local().Format("[15:04:05]")
		} else if t, err := time.Parse(time.RFC3339, e.Time); err == nil {
			timeStr = t.Local().Format("[15:04:05]")
		} else {
			timeStr = ts()
		}
	} else {
		timeStr = ts()
	}

	// Level coloring.
	levelStr := levelColored(e.Level)

	// Build the main line.
	var parts []string
	parts = append(parts, timeStr)
	parts = append(parts, badge)
	if shortID != "" {
		parts = append(parts, fmt.Sprintf("%s%s%s", colorDim, shortID, colorReset))
	}
	parts = append(parts, levelStr)

	if e.Controller != "" {
		parts = append(parts, e.Controller)
	}
	if e.Name != "" {
		// Show kind/name (e.g. rsc/test-batch-13) instead of bare name.
		displayName := e.Name
		if e.Kind != "" {
			displayName = shortKindFor(e.Kind) + "/" + e.Name
		}
		parts = append(parts, fmt.Sprintf("%s%s%s", colorBold, displayName, colorReset))
	}

	// Message with phase-specific formatting.
	msgFormatted := formatMessage(e)
	parts = append(parts, msgFormatted)

	lines = append(lines, strings.Join(parts, " "))
	return lines
}

func shortReconcileID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func levelColored(level string) string {
	switch {
	case strings.EqualFold(level, "ERROR"):
		return fmt.Sprintf("%sERROR%s", colorBoldRed, colorReset)
	case strings.EqualFold(level, "WARN") || strings.EqualFold(level, "WARNING"):
		return fmt.Sprintf("%sWARN %s", colorYellow, colorReset)
	case strings.EqualFold(level, "INFO"):
		return "INFO "
	case strings.EqualFold(level, "DEBUG"):
		return fmt.Sprintf("%sDEBUG%s", colorDim, colorReset)
	default:
		// slogh outputs non-standard levels as raw numbers: "-1", "-5", "-8", etc.
		// Map to V<N> where N is the logr verbosity (negate the slog level).
		if n, err := strconv.Atoi(level); err == nil && n < 0 {
			return fmt.Sprintf("%s%-5s%s", colorDim, fmt.Sprintf("V%d", -n), colorReset)
		}
		return fmt.Sprintf("%-5s", level)
	}
}

func formatMessage(e *logEntry) string {
	// Phase-specific formatting.
	if e.Msg == "phase start" {
		return formatPhaseStart(e)
	}

	if e.Msg == "phase end" {
		return formatPhaseEnd(e)
	}

	// Generic message with phase path context (for log lines within a phase).
	var parts []string

	// Show phase path context for non-phase log lines within a phase.
	if len(e.PhasePath) > 0 {
		pathStr := strings.Join(e.PhasePath, " › ")
		parts = append(parts, fmt.Sprintf("%s[%s]%s", colorDim, pathStr, colorReset))
	}

	parts = append(parts, e.Msg)

	// Append error if present.
	if e.Error != "" {
		parts = append(parts, fmt.Sprintf("%serr=%q%s", colorRed, e.Error, colorReset))
	}

	// Append extras.
	if extras := formatExtras(e); extras != "" {
		parts = append(parts, extras)
	}

	return strings.Join(parts, "  ")
}

func formatPhaseStart(e *logEntry) string {
	// Build the phase path display with tree characters.
	depth := len(e.PhasePath)
	indent := ""
	if depth > 1 {
		indent = strings.Repeat("│ ", depth-1)
	}

	phase := e.PhaseName
	if phase == "" {
		phase = "?"
	}

	extraStr := ""
	if extras := formatExtras(e); extras != "" {
		extraStr = "  " + extras
	}

	return fmt.Sprintf("%s%s▶ %s%s%s", colorDim, indent, phase, colorReset, extraStr)
}

func formatPhaseEnd(e *logEntry) string {
	phase := e.PhaseName
	if phase == "" {
		phase = "?"
	}

	// Build indentation based on phase path depth.
	depth := len(e.PhasePath)
	indent := ""
	if depth > 1 {
		indent = strings.Repeat("│ ", depth-1)
	}

	var parts []string

	// Result with color.
	if e.Result != "" {
		parts = append(parts, colorizeResult(e.Result))
	}

	if e.Changed == "true" {
		parts = append(parts, fmt.Sprintf("%schanged%s", colorYellow, colorReset))
	}

	if e.HasError == "true" || e.Error != "" {
		errMsg := e.Error
		if errMsg == "" {
			errMsg = "true"
		}
		parts = append(parts, fmt.Sprintf("%serr=%s%s", colorRed, errMsg, colorReset))
	}

	if e.Duration != "" {
		parts = append(parts, fmt.Sprintf("%s%s%s", colorDim, e.Duration, colorReset))
	}

	// Append extras.
	if extras := formatExtras(e); extras != "" {
		parts = append(parts, extras)
	}

	if len(parts) == 0 {
		return fmt.Sprintf("%s%s■ %s%s", colorDim, indent, phase, colorReset)
	}

	return fmt.Sprintf("%s■ %s  %s", indent, phase, strings.Join(parts, "  "))
}

// formatExtras renders extra key-value pairs as dim key=value tokens.
func formatExtras(e *logEntry) string {
	if len(e.extras) == 0 {
		return ""
	}
	var parts []string
	for _, kv := range e.extras {
		val := kv.val
		if kv.file != "" {
			// Make the value a clickable link to the saved file.
			val = osc8Link(val, kv.file)
		}
		parts = append(parts, fmt.Sprintf("%s%s=%s%s%s", colorDim, kv.key, colorReset, val, colorReset))
	}
	return strings.Join(parts, " ")
}

func colorizeResult(result string) string {
	switch result {
	case "Continue", "Done":
		return fmt.Sprintf("%s%s%s", colorGreen, result, colorReset)
	case "Fail":
		return fmt.Sprintf("%s%s%s", colorBoldRed, result, colorReset)
	default:
		if strings.Contains(result, "Requeue") {
			return fmt.Sprintf("%s%s%s", colorYellow, result, colorReset)
		}
		return result
	}
}

// ---------------------------------------------------------------------------
// Unified diff (pure stdlib, no dependencies)
// ---------------------------------------------------------------------------

const (
	opEqual = iota
	opReplace
	opInsert
	opDelete
)

type opcode struct {
	tag    int
	i1, i2 int
	j1, j2 int
}

func unifiedDiff(a, b []string) []string {
	ops := diffOpcodes(a, b)

	const ctx = 0
	type group struct{ ops []opcode }
	var groups []group

	for _, op := range ops {
		if op.tag == opEqual {
			continue
		}
		if len(groups) == 0 {
			groups = append(groups, group{})
		}
		last := &groups[len(groups)-1]
		if len(last.ops) > 0 {
			prev := last.ops[len(last.ops)-1]
			gap := op.i1 - prev.i2
			if g := op.j1 - prev.j2; g > gap {
				gap = g
			}
			if gap > 2*ctx {
				groups = append(groups, group{})
				last = &groups[len(groups)-1]
			}
		}
		last.ops = append(last.ops, op)
	}

	if len(groups) == 0 {
		return nil
	}

	var out []string
	prevPath := ""

	for _, g := range groups {
		first := g.ops[0]
		lastOp := g.ops[len(g.ops)-1]

		i1 := max(first.i1-ctx, 0)
		i2 := min(lastOp.i2+ctx, len(a))
		j1 := max(first.j1-ctx, 0)
		j2 := min(lastOp.j2+ctx, len(b))

		// Show YAML path breadcrumb instead of raw @@ line numbers.
		// Suppress duplicate consecutive paths.
		path := yamlPath(b, first.j1)
		if path != "" && path != prevPath {
			out = append(out, fmt.Sprintf("── %s", path))
		} else if path == "" {
			out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", i1+1, i2-i1, j1+1, j2-j1))
		}
		prevPath = path

		ia, ib := i1, j1
		for _, op := range g.ops {
			for ia < op.i1 && ib < op.j1 {
				out = append(out, " "+a[ia])
				ia++
				ib++
			}
			switch op.tag {
			case opReplace:
				for i := op.i1; i < op.i2; i++ {
					out = append(out, "-"+a[i])
				}
				for j := op.j1; j < op.j2; j++ {
					out = append(out, "+"+b[j])
				}
			case opDelete:
				for i := op.i1; i < op.i2; i++ {
					out = append(out, "-"+a[i])
				}
			case opInsert:
				for j := op.j1; j < op.j2; j++ {
					out = append(out, "+"+b[j])
				}
			}
			ia, ib = op.i2, op.j2
		}
		for ia < i2 && ib < j2 {
			out = append(out, " "+a[ia])
			ia++
			ib++
		}
	}

	return out
}

// yamlPath computes the YAML key path leading to line idx in lines.
// It walks backwards to find parent keys at decreasing indentation levels.
// Returns e.g. "metadata.finalizers" or "spec".
func yamlPath(lines []string, idx int) string {
	if idx < 0 || idx >= len(lines) {
		return ""
	}

	// Find the indentation of the target line.
	targetIndent := yamlIndent(lines[idx])

	// Walk backwards, collecting parent keys at decreasing indentation.
	var parts []string
	needIndent := targetIndent
	for i := idx; i >= 0; i-- {
		line := lines[i]
		indent := yamlIndent(line)

		if indent >= needIndent && i != idx {
			continue
		}

		// Extract YAML key from this line (the part before ":").
		key := yamlKeyFromLine(line)
		if key == "" {
			continue
		}

		// Only accept lines with strictly less indentation (true parent).
		if indent < needIndent {
			parts = append(parts, key)
			needIndent = indent
		}

		if indent == 0 {
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}

	// Reverse to get root-first order.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ".")
}

// yamlIndent returns the number of leading spaces in a YAML line.
func yamlIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// yamlKeyFromLine extracts the YAML mapping key from a line like "  foo:" or "  foo: bar".
// Returns "" for list items ("- ...") or lines without a key.
func yamlKeyFromLine(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	// Skip list items.
	if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
		return ""
	}
	if idx := strings.Index(trimmed, ":"); idx > 0 {
		return trimmed[:idx]
	}
	return ""
}

func diffOpcodes(a, b []string) []opcode {
	n, m := len(a), len(b)

	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var raw []opcode
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			raw = append(raw, opcode{opEqual, i, i + 1, j, j + 1})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			raw = append(raw, opcode{opDelete, i, i + 1, j, j})
			i++
		} else {
			raw = append(raw, opcode{opInsert, i, i, j, j + 1})
			j++
		}
	}
	for i < n {
		raw = append(raw, opcode{opDelete, i, i + 1, j, j})
		i++
	}
	for j < m {
		raw = append(raw, opcode{opInsert, i, i, j, j + 1})
		j++
	}

	if len(raw) == 0 {
		return nil
	}

	merged := []opcode{raw[0]}
	for _, op := range raw[1:] {
		last := &merged[len(merged)-1]
		if op.tag == last.tag {
			last.i2 = op.i2
			last.j2 = op.j2
		} else {
			merged = append(merged, op)
		}
	}

	var result []opcode
	for k := 0; k < len(merged); k++ {
		if k+1 < len(merged) && merged[k].tag == opDelete && merged[k+1].tag == opInsert {
			result = append(result, opcode{opReplace, merged[k].i1, merged[k].i2, merged[k+1].j1, merged[k+1].j2})
			k++
		} else {
			result = append(result, merged[k])
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Usage and main
// ---------------------------------------------------------------------------

func usage() {
	fmt.Fprintf(os.Stderr, "usage: go run hack/debug.go [--log=<path>] [--snapshots=<dir>] <target> [<target> ...]\n")
	fmt.Fprintf(os.Stderr, "\ntargets:\n")
	fmt.Fprintf(os.Stderr, "  <kind>            watch all objects of that kind\n")
	fmt.Fprintf(os.Stderr, "  <kind>/<name>     watch a specific named object\n")
	fmt.Fprintf(os.Stderr, "\nflags:\n")
	fmt.Fprintf(os.Stderr, "  --log=<path>        write plain-text copy of output to file\n")
	fmt.Fprintf(os.Stderr, "  --snapshots=<dir>   save full object JSON snapshots; names become clickable links\n")
	fmt.Fprintf(os.Stderr, "\nexamples:\n")
	fmt.Fprintf(os.Stderr, "  go run hack/debug.go rsc rsp\n")
	fmt.Fprintf(os.Stderr, "  go run hack/debug.go rsc/my-storage-class rv\n")
	fmt.Fprintf(os.Stderr, "  go run hack/debug.go --log=/tmp/debug.log --snapshots=/tmp/snaps rsc/my-sc rsp\n")
	fmt.Fprintf(os.Stderr, "  KUBECONFIG=~/.kube/my-config go run hack/debug.go rsc\n")
	os.Exit(2)
}

func main() {
	var logPath string
	var rawTargets []string

	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--log=") {
			logPath = strings.TrimPrefix(arg, "--log=")
		} else if strings.HasPrefix(arg, "--snapshots=") {
			snapshotsDir = strings.TrimPrefix(arg, "--snapshots=")
		} else if arg == "--help" || arg == "-h" {
			usage()
		} else {
			rawTargets = append(rawTargets, arg)
		}
	}

	if len(rawTargets) == 0 {
		usage()
	}

	if logPath != "" {
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot open log %s: %v\n", logPath, err)
		} else {
			output.log = logFile
			defer logFile.Close()
		}
	}

	if snapshotsDir != "" {
		if err := os.MkdirAll(snapshotsDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot create snapshots dir %s: %v\n", snapshotsDir, err)
			snapshotsDir = ""
		}
	}

	targets := parseTargets(rawTargets)
	ws := buildWatchSet(targets)

	// Display what we're watching.
	var watchDesc []string
	for _, t := range targets {
		if t.name != "" {
			watchDesc = append(watchDesc, t.kind+"/"+t.name)
		} else {
			watchDesc = append(watchDesc, t.kind)
		}
	}
	emit(fmt.Sprintf("\n%s ── started, watching: %s ──", ts(), strings.Join(watchDesc, ", ")))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	// Start resource watchers.
	// Deduplicate kinds: for each kind, collect specific names.
	kindSpecificNames := map[string][]string{}
	for _, t := range targets {
		if t.name != "" {
			kindSpecificNames[t.kind] = append(kindSpecificNames[t.kind], t.name)
		}
	}

	for _, kind := range ws.allKinds {
		wg.Add(1)
		names := kindSpecificNames[kind]
		go watchResource(ctx, kind, names, &ws, &wg)
	}

	// Start log streamers for controller and agent.
	wg.Add(2)
	go followPodLogs(ctx, "controller", "app=controller", &ws, &wg)
	go followPodLogs(ctx, "agent", "app=agent", &ws, &wg)

	wg.Wait()
}
