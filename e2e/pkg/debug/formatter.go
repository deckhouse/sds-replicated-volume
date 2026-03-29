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
)

// Formatter formats debug output for watch events and log entries.
// Default implementation: ColorFormatter (ANSI). Use PlainFormatter for
// output without ANSI escape codes.
type Formatter interface {
	FormatAdded(kind, objLink string, condLines, yamlLines []string) []string
	FormatModified(kind, objLink string, condLines, diffLines []string) []string
	FormatDeleted(kind, objLink string) string
	FormatLogEntry(entry *LogEntry, component string, tracker *ReconcileIDTracker) []string
}

// ColorFormatter produces ANSI-colored output. When used with an eventHandler
// whose RenderConfig has Colors: true, condition tables are pre-rendered with
// ANSI codes and the formatter adds its own colored headers and box drawing.
type ColorFormatter struct {
	kr *KindRegistry
}

var colorCfg = RenderConfig{Colors: true}
var plainCfg = RenderConfig{Colors: false}

func (f *ColorFormatter) FormatAdded(kind, objLink string, condLines, yamlLines []string) []string {
	return formatAdded(kind, objLink, condLines, yamlLines, palette(colorCfg))
}

func (f *ColorFormatter) FormatModified(kind, objLink string, condLines, diffLines []string) []string {
	return formatModified(kind, objLink, condLines, diffLines, palette(colorCfg))
}

func (f *ColorFormatter) FormatDeleted(kind, objLink string) string {
	return formatDeleted(kind, objLink, palette(colorCfg))
}

func (f *ColorFormatter) FormatLogEntry(entry *LogEntry, component string, tracker *ReconcileIDTracker) []string {
	return formatLogEntryTracked(entry, component, tracker, palette(colorCfg), f.kr)
}

// PlainFormatter produces output identical in structure to ColorFormatter but
// without ANSI escape codes. Condition tables and log entries are rendered
// plain from the start — no post-hoc stripping required.
type PlainFormatter struct {
	kr *KindRegistry
}

func (f *PlainFormatter) FormatAdded(kind, objLink string, condLines, yamlLines []string) []string {
	return formatAdded(kind, objLink, condLines, yamlLines, palette(plainCfg))
}

func (f *PlainFormatter) FormatModified(kind, objLink string, condLines, diffLines []string) []string {
	return formatModified(kind, objLink, condLines, diffLines, palette(plainCfg))
}

func (f *PlainFormatter) FormatDeleted(kind, objLink string) string {
	return formatDeleted(kind, objLink, palette(plainCfg))
}

func (f *PlainFormatter) FormatLogEntry(entry *LogEntry, component string, tracker *ReconcileIDTracker) []string {
	return formatLogEntryTracked(entry, component, tracker, palette(plainCfg), f.kr)
}

// ---------------------------------------------------------------------------
// Shared formatting implementation
// ---------------------------------------------------------------------------

func formatAdded(kind, objLink string, condLines, yamlLines []string, p colorPalette) []string {
	block := []string{fmt.Sprintf("%s %s[%s]%s %sADDED%s %s",
		Ts(), p.cyan, kind, p.reset, p.green, p.reset, objLink)}

	hasConds := len(condLines) > 0
	if hasConds {
		block = append(block, condLines...)
		block = append(block, fmt.Sprintf("  %s├──%s", p.dim, p.reset))
	} else {
		block = append(block, fmt.Sprintf("  %s┌%s", p.dim, p.reset))
	}
	for _, l := range yamlLines {
		block = append(block, fmt.Sprintf("  %s│%s %s%s%s", p.dim, p.reset, p.dim, l, p.reset))
	}
	block = append(block, fmt.Sprintf("  %s└%s", p.dim, p.reset))
	return block
}

func formatModified(kind, objLink string, condLines, diffLines []string, p colorPalette) []string {
	block := []string{fmt.Sprintf("%s %s[%s]%s MODIFIED %s",
		Ts(), p.cyan, kind, p.reset, objLink)}

	hasConds := len(condLines) > 0
	hasDiff := len(diffLines) > 0
	block = append(block, condLines...)

	if hasDiff {
		if hasConds {
			block = append(block, fmt.Sprintf("  %s├──%s", p.dim, p.reset))
		} else {
			block = append(block, fmt.Sprintf("  %s┌%s", p.dim, p.reset))
		}
		bar := fmt.Sprintf("  %s│%s ", p.dim, p.reset)
		for _, d := range diffLines {
			switch {
			case strings.HasPrefix(d, "+"):
				block = append(block, fmt.Sprintf("%s%s%s%s", bar, p.green, d, p.reset))
			case strings.HasPrefix(d, "-"):
				block = append(block, fmt.Sprintf("%s%s%s%s", bar, p.red, d, p.reset))
			case strings.HasPrefix(d, "@@") || strings.HasPrefix(d, "──"):
				block = append(block, fmt.Sprintf("%s%s%s%s", bar, p.cyan, d, p.reset))
			default:
				block = append(block, fmt.Sprintf("%s%s", bar, d))
			}
		}
	}
	block = append(block, fmt.Sprintf("  %s└%s", p.dim, p.reset))
	return block
}

func formatDeleted(kind, objLink string, p colorPalette) string {
	return fmt.Sprintf("%s %s[%s]%s %sDELETED%s %s",
		Ts(), p.cyan, kind, p.reset, p.red, p.reset, objLink)
}
