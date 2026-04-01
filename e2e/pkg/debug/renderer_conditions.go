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
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Terminal utilities
// ---------------------------------------------------------------------------

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

// truncMsg truncates a message to maxW characters, appending "..." if needed.
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

// pad right-pads s with spaces to width w.
func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// ---------------------------------------------------------------------------
// Condition column width computation
// ---------------------------------------------------------------------------

// condWidths computes column widths from actual condition data.
func condWidths(sets ...[]Condition) (typeW, statusW, reasonW int) {
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

const condPrefixWidth = 9 // display chars in "  │ X ⊙✓ " before variable columns

// condMsgMaxWidth returns the maximum message width given column widths.
func condMsgMaxWidth(aw, tw, sw, rw int) int {
	prefix := condPrefixWidth + aw + 1 + tw + 1 + sw + 1 + rw + 1
	w := termWidth() - prefix
	if w < 20 {
		w = 20
	}
	return w
}

// condAge returns a short human-readable age string for a condition's
// lastTransitionTime (e.g. "3s", "5m", "2h", "7d"). Returns "⏱--" if absent.
func condAge(c Condition) string {
	if c.LastTransitionTime == "" {
		return "⏱--"
	}
	t, err := time.Parse(time.RFC3339, c.LastTransitionTime)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, c.LastTransitionTime)
		if err != nil {
			return "⏱?"
		}
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("⏱%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("⏱%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("⏱%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("⏱%dd", int(d.Hours()/24))
	}
}

// condAgeWidth computes the max display width of the age column across
// multiple condition sets.
const minAgeWidth = 2

func condAgeWidth(sets ...[]Condition) int {
	w := minAgeWidth
	for _, cs := range sets {
		for _, c := range cs {
			if aw := len(condAge(c)); aw > w {
				w = aw
			}
		}
	}
	return w
}

// ---------------------------------------------------------------------------
// Status and generation icons
// ---------------------------------------------------------------------------

// GenerationIcon returns a colored icon indicating whether a condition's
// observedGeneration matches the object's generation.
//
//	"∅" (red)    — observedGeneration absent (not set)
//	"⊙" (green)  — observedGeneration == generation (current)
//	"◌" (yellow) — observedGeneration != generation (stale)
func GenerationIcon(c Condition, cfg RenderConfig) string {
	p := palette(cfg)
	if c.ObservedGeneration < 0 {
		return fmt.Sprintf("%s∅%s", p.red, p.reset)
	}
	if c.ObservedGeneration == c.Generation {
		return fmt.Sprintf("%s⊙%s", p.green, p.reset)
	}
	return fmt.Sprintf("%s◌%s", p.yellow, p.reset)
}

// GenerationIconDim returns a plain generation icon character without ANSI
// codes — the caller is responsible for wrapping in ColorDim.
func GenerationIconDim(c Condition) string {
	if c.ObservedGeneration < 0 {
		return "∅"
	}
	if c.ObservedGeneration == c.Generation {
		return "⊙"
	}
	return "◌"
}

// StatusIcon returns a colored icon for a condition status.
func StatusIcon(status string, cfg RenderConfig) string {
	p := palette(cfg)
	switch status {
	case "True":
		return fmt.Sprintf("%s✓%s", p.green, p.reset)
	case "False":
		return fmt.Sprintf("%s✗%s", p.red, p.reset)
	default:
		return fmt.Sprintf("%s?%s", p.yellow, p.reset)
	}
}

// StatusIconDim returns a plain status icon (for unchanged conditions).
func StatusIconDim(status string) string {
	switch status {
	case "True":
		return "✓"
	case "False":
		return "✗"
	default:
		return "?"
	}
}

// StatusColored returns the status string with color. Preserves original
// width with padding after the ANSI codes.
func StatusColored(status string, cfg RenderConfig) string {
	p := palette(cfg)
	trimmed := strings.TrimSpace(status)
	var colored string
	switch trimmed {
	case "True":
		colored = fmt.Sprintf("%sTrue%s", p.green, p.reset)
	case "False":
		colored = fmt.Sprintf("%sFalse%s", p.red, p.reset)
	default:
		colored = fmt.Sprintf("%s%s%s", p.yellow, trimmed, p.reset)
	}
	if extra := len(status) - len(trimmed); extra > 0 {
		colored += strings.Repeat(" ", extra)
	}
	return colored
}

// ---------------------------------------------------------------------------
// Conditions table rendering
// ---------------------------------------------------------------------------

// ConditionsTableNew returns formatted lines for a conditions table of a newly
// ADDED object. Does NOT include the closing └ — the caller handles border
// transitions.
func ConditionsTableNew(newConds []Condition, cfg RenderConfig) []string {
	p := palette(cfg)
	tw, sw, rw := condWidths(newConds)
	aw := condAgeWidth(newConds)
	mw := condMsgMaxWidth(aw, tw, sw, rw)
	lines := []string{fmt.Sprintf("  %s┌ conditions%s", p.dim, p.reset)}
	for _, c := range newConds {
		lines = append(lines, fmt.Sprintf("  %s│   %s%s %s %s %s %s %s%s",
			p.dim,
			GenerationIconDim(c),
			StatusIconDim(c.Status),
			pad(condAge(c), aw),
			pad(c.Type, tw),
			pad(c.Status, sw),
			pad(c.Reason, rw),
			truncMsg(c.Message, mw), p.reset,
		))
	}
	return lines
}

// ConditionsTableDiff returns formatted lines showing the conditions diff.
// Returns nil when there are no conditions at all and nothing changed.
// Does NOT include the closing └ — the caller handles border transitions.
func ConditionsTableDiff(oldConds, newConds []Condition, cfg RenderConfig) []string {
	p := palette(cfg)
	oldMap := make(map[string]Condition, len(oldConds))
	for _, c := range oldConds {
		oldMap[c.Type] = c
	}
	newMap := make(map[string]Condition, len(newConds))
	for _, c := range newConds {
		newMap[c.Type] = c
	}

	changed := !ConditionsEqual(oldConds, newConds)

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

	aw := condAgeWidth(oldConds, newConds)
	mw := condMsgMaxWidth(aw, tw, sw, rw)

	var lines []string

	// When nothing changed, render the whole table in dim.
	if !changed {
		lines = append(lines, fmt.Sprintf("  %s┌ conditions (unchanged)%s", p.dim, p.reset))
		for _, c := range newConds {
			lines = append(lines, fmt.Sprintf("  %s│   %s%s %s %s %s %s %s%s",
				p.dim,
				GenerationIconDim(c),
				StatusIconDim(c.Status),
				pad(condAge(c), aw),
				pad(c.Type, tw),
				pad(c.Status, sw),
				pad(c.Reason, rw),
				truncMsg(c.Message, mw), p.reset,
			))
		}
		return lines
	}

	lines = append(lines, fmt.Sprintf("  %s┌ conditions%s", p.dim, p.reset))

	for _, c := range newConds {
		old, existed := oldMap[c.Type]
		lines = append(lines, condDiffRow(c, old, existed, aw, tw, sw, rw, mw, cfg))
	}

	for _, c := range oldConds {
		if _, exists := newMap[c.Type]; !exists {
			lines = append(lines, condRemovedRow(c, aw, tw, cfg))
		}
	}

	return lines
}

// condDiffRow renders a single condition row within a diff table, choosing
// the appropriate format based on whether the condition is new, changed, or
// unchanged.
func condDiffRow(c, old Condition, existed bool, aw, tw, sw, rw, mw int, cfg RenderConfig) string {
	p := palette(cfg)
	switch {
	case !existed:
		return fmt.Sprintf("  %s│%s %s %s%s %s %s %s %s %s%s%s",
			p.dim, p.reset,
			p.green+"+"+p.reset,
			GenerationIcon(c, cfg),
			StatusIcon(c.Status, cfg),
			pad(condAge(c), aw),
			pad(c.Type, tw),
			StatusColored(pad(c.Status, sw), cfg),
			pad(c.Reason, rw),
			p.dim, truncMsg(c.Message, mw), p.reset,
		)
	case CondChanged(old, c):
		statusStr := StatusColored(pad(c.Status, sw), cfg)
		if old.Status != c.Status {
			plainW := len(old.Status) + 1 + len(c.Status)
			statusStr = fmt.Sprintf("%s→%s", StatusColored(old.Status, cfg), StatusColored(c.Status, cfg))
			if plainW < sw {
				statusStr += strings.Repeat(" ", sw-plainW)
			}
		}
		reasonStr := pad(c.Reason, rw)
		if old.Reason != c.Reason {
			plainW := len(old.Reason) + 1 + len(c.Reason)
			reasonStr = fmt.Sprintf("%s%s%s→%s", p.dim, old.Reason, p.reset, c.Reason)
			if plainW < rw {
				reasonStr += strings.Repeat(" ", rw-plainW)
			}
		}
		ageStr := pad(condAge(c), aw)
		if old.LastTransitionTime != c.LastTransitionTime {
			ageStr = fmt.Sprintf("%s%s%s", p.yellow, pad(condAge(c), aw), p.reset)
		}
		return fmt.Sprintf("  %s│%s %s %s%s %s %s %s %s %s%s%s",
			p.dim, p.reset,
			p.yellow+"~"+p.reset,
			GenerationIcon(c, cfg),
			StatusIcon(c.Status, cfg),
			ageStr,
			pad(c.Type, tw),
			statusStr,
			reasonStr,
			p.dim, truncMsg(c.Message, mw), p.reset,
		)
	default:
		return fmt.Sprintf("  %s│   %s%s %s %s %s %s %s%s",
			p.dim,
			GenerationIconDim(c),
			StatusIconDim(c.Status),
			pad(condAge(c), aw),
			pad(c.Type, tw),
			pad(c.Status, sw),
			pad(c.Reason, rw),
			truncMsg(c.Message, mw), p.reset,
		)
	}
}

// condRemovedRow renders a row for a condition that was removed.
func condRemovedRow(c Condition, aw, tw int, cfg RenderConfig) string {
	p := palette(cfg)
	return fmt.Sprintf("  %s│%s %s  %s %s %s%s (removed)%s",
		p.dim, p.reset,
		p.red+"-"+p.reset,
		StatusIcon(c.Status, cfg),
		pad(condAge(c), aw),
		p.red, pad(c.Type, tw), p.reset,
	)
}
