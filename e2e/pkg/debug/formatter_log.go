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
	"strconv"
	"strings"
	"time"
)

// shortIDLen is the number of characters kept when truncating reconcile IDs.
const shortIDLen = 8

// ---------------------------------------------------------------------------
// Timestamp
// ---------------------------------------------------------------------------

// Ts returns a formatted timestamp string for log output.
// Uses [HH:MM:SS.mmm] format with millisecond precision.
func Ts() string {
	return time.Now().Format("[15:04:05.000]")
}

// ---------------------------------------------------------------------------
// Log visualization
// ---------------------------------------------------------------------------

// BadgeColor returns the ANSI color for a component badge.
func BadgeColor(component string) string {
	return badgeColor(component, activePalette())
}

func badgeColor(component string, p colorPalette) string {
	switch component {
	case "controller":
		return p.magenta
	case "agent":
		return p.yellow
	default:
		return p.cyan
	}
}

// FormatLogEntryTracked formats a log entry with reconcile boundary tracking.
// It returns a slice of formatted lines suitable for EmitBlock.
func FormatLogEntryTracked(e *LogEntry, component string, tracker *ReconcileIDTracker) []string {
	return formatLogEntryTracked(e, component, tracker, activePalette(), defaultKindRegistry)
}

func formatLogEntryTracked(e *LogEntry, component string, tracker *ReconcileIDTracker, p colorPalette, kr *KindRegistry) []string {
	var lines []string

	badge := fmt.Sprintf("%s[%s]%s", badgeColor(component, p), component, p.reset)

	shortID := ""
	if e.ReconcileID != "" {
		shortID = e.ReconcileID
		if len(shortID) > shortIDLen {
			shortID = shortID[:shortIDLen]
		}
	}

	if e.Controller != "" && e.Name != "" && e.ReconcileID != "" {
		key := e.Controller + "\x00" + e.Name
		prev, hasPrev := tracker.Swap(key, e.ReconcileID)
		if hasPrev && prev != e.ReconcileID {
			lines = append(lines, fmt.Sprintf(
				"%s────────── reconcile %s done ──────────%s",
				p.dim, ShortReconcileID(prev), p.reset))
		}
	}

	return formatLogEntryCommon(e, badge, shortID, lines, p, kr)
}

func formatLogEntryCommon(e *LogEntry, badge, shortID string, lines []string, p colorPalette, kr *KindRegistry) []string {
	timeStr := ""
	if e.Time != "" {
		if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
			timeStr = t.Local().Format("[15:04:05.000]")
		} else if t, err := time.Parse(time.RFC3339, e.Time); err == nil {
			timeStr = t.Local().Format("[15:04:05.000]")
		} else {
			timeStr = Ts()
		}
	} else {
		timeStr = Ts()
	}

	levelStr := levelColored(e.Level, p)

	var parts []string
	parts = append(parts, timeStr)
	parts = append(parts, badge)
	if shortID != "" {
		parts = append(parts, fmt.Sprintf("%s%s%s", p.dim, shortID, p.reset))
	}
	parts = append(parts, levelStr)

	if e.Controller != "" {
		parts = append(parts, e.Controller)
	}
	if e.Name != "" {
		displayName := e.Name
		if e.Kind != "" {
			displayName = kr.ShortFor(e.Kind) + "/" + e.Name
		}
		parts = append(parts, fmt.Sprintf("%s%s%s", p.bold, displayName, p.reset))
	}

	msgFormatted := formatMessage(e, p)
	parts = append(parts, msgFormatted)

	lines = append(lines, strings.Join(parts, " "))
	return lines
}

// ShortReconcileID truncates a reconcile ID to shortIDLen characters.
func ShortReconcileID(id string) string {
	if len(id) > shortIDLen {
		return id[:shortIDLen]
	}
	return id
}

func levelColored(level string, p colorPalette) string {
	switch {
	case strings.EqualFold(level, "ERROR"):
		return fmt.Sprintf("%sERROR%s", p.boldRed, p.reset)
	case strings.EqualFold(level, "WARN") || strings.EqualFold(level, "WARNING"):
		return fmt.Sprintf("%sWARN %s", p.yellow, p.reset)
	case strings.EqualFold(level, "INFO"):
		return "INFO "
	case strings.EqualFold(level, "DEBUG"):
		return fmt.Sprintf("%sDEBUG%s", p.dim, p.reset)
	default:
		if n, err := strconv.Atoi(level); err == nil && n < 0 {
			return fmt.Sprintf("%s%-5s%s", p.dim, fmt.Sprintf("V%d", -n), p.reset)
		}
		return fmt.Sprintf("%-5s", level)
	}
}

func formatMessage(e *LogEntry, p colorPalette) string {
	if e.Msg == "phase start" {
		return formatPhaseStart(e, p)
	}

	if e.Msg == "phase end" {
		return formatPhaseEnd(e, p)
	}

	var parts []string

	if len(e.PhasePath) > 0 {
		pathStr := strings.Join(e.PhasePath, " › ")
		parts = append(parts, fmt.Sprintf("%s[%s]%s", p.dim, pathStr, p.reset))
	}

	parts = append(parts, e.Msg)

	if e.Error != "" {
		parts = append(parts, fmt.Sprintf("%serr=%q%s", p.red, e.Error, p.reset))
	}

	if extras := formatExtras(e, p); extras != "" {
		parts = append(parts, extras)
	}

	return strings.Join(parts, "  ")
}

func formatPhaseStart(e *LogEntry, p colorPalette) string {
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
	if extras := formatExtras(e, p); extras != "" {
		extraStr = "  " + extras
	}

	return fmt.Sprintf("%s%s▶ %s%s%s", p.dim, indent, phase, p.reset, extraStr)
}

func formatPhaseEnd(e *LogEntry, p colorPalette) string {
	phase := e.PhaseName
	if phase == "" {
		phase = "?"
	}

	depth := len(e.PhasePath)
	indent := ""
	if depth > 1 {
		indent = strings.Repeat("│ ", depth-1)
	}

	var parts []string

	if e.Result != "" {
		parts = append(parts, colorizeResult(e.Result, p))
	}

	if e.Changed == "true" {
		parts = append(parts, fmt.Sprintf("%schanged%s", p.yellow, p.reset))
	}

	if e.HasError == "true" || e.Error != "" {
		errMsg := e.Error
		if errMsg == "" {
			errMsg = "true"
		}
		parts = append(parts, fmt.Sprintf("%serr=%s%s", p.red, errMsg, p.reset))
	}

	if e.Duration != "" {
		parts = append(parts, fmt.Sprintf("%s%s%s", p.dim, e.Duration, p.reset))
	}

	if extras := formatExtras(e, p); extras != "" {
		parts = append(parts, extras)
	}

	if len(parts) == 0 {
		return fmt.Sprintf("%s%s■ %s%s", p.dim, indent, phase, p.reset)
	}

	return fmt.Sprintf("%s■ %s  %s", indent, phase, strings.Join(parts, "  "))
}

func formatExtras(e *LogEntry, p colorPalette) string {
	if len(e.Extras) == 0 {
		return ""
	}
	var parts []string
	for _, kv := range e.Extras {
		val := kv.Val
		if kv.File != "" {
			val = OSC8Link(val, kv.File)
		}
		parts = append(parts, fmt.Sprintf("%s%s=%s%s%s", p.dim, kv.Key, p.reset, val, p.reset))
	}
	return strings.Join(parts, " ")
}

func colorizeResult(result string, p colorPalette) string {
	switch result {
	case "Continue", "Done":
		return fmt.Sprintf("%s%s%s", p.green, result, p.reset)
	case "Fail":
		return fmt.Sprintf("%s%s%s", p.boldRed, result, p.reset)
	default:
		if strings.Contains(result, "Requeue") {
			return fmt.Sprintf("%s%s%s", p.yellow, result, p.reset)
		}
		return result
	}
}
