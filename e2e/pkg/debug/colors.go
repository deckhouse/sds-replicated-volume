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
	"os"
	"regexp"
)

// ColorsEnabled reports whether ANSI color output is active. It is false when
// the NO_COLOR environment variable is set (https://no-color.org/).
var ColorsEnabled bool

// ANSI escape sequences for colored terminal output. When ColorsEnabled is
// false (NO_COLOR set), all values are empty strings.
var (
	ColorRed       string
	ColorGreen     string
	ColorYellow    string
	ColorCyan      string
	ColorMagenta   string
	ColorDim       string
	ColorBold      string
	ColorBoldRed   string
	ColorBoldGreen string
	ColorReset     string
)

// AnsiRe matches ANSI SGR escape sequences for stripping colors from output.
var AnsiRe = regexp.MustCompile(`\033\[[0-9;]*m`)

func init() {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		ColorsEnabled = false
	} else {
		ColorsEnabled = true
		ColorRed = "\033[31m"
		ColorGreen = "\033[32m"
		ColorYellow = "\033[33m"
		ColorCyan = "\033[36m"
		ColorMagenta = "\033[35m"
		ColorDim = "\033[2m"
		ColorBold = "\033[1m"
		ColorBoldRed = "\033[1;31m"
		ColorBoldGreen = "\033[1;32m"
		ColorReset = "\033[0m"
	}
}

// DisableColors programmatically disables ANSI color output by clearing all
// color variables. This mutates process-global state and should be called
// once, early in main(), before creating any Debugger instances.
func DisableColors() {
	ColorsEnabled = false
	ColorRed = ""
	ColorGreen = ""
	ColorYellow = ""
	ColorCyan = ""
	ColorMagenta = ""
	ColorDim = ""
	ColorBold = ""
	ColorBoldRed = ""
	ColorBoldGreen = ""
	ColorReset = ""
}

// RenderConfig controls whether rendering functions produce ANSI color codes.
// Pass to ConditionsTableNew, ConditionsTableDiff, and related renderers so
// they produce correctly colored or plain output from the start — no
// post-hoc stripping required.
type RenderConfig struct {
	Colors bool
}

// colorPalette holds resolved ANSI escape sequences (or empty strings when
// colors are disabled). Obtain via palette().
type colorPalette struct {
	red, green, yellow, cyan, magenta string
	dim, bold, boldRed, boldGreen     string
	reset                             string
}

// palette returns a colorPalette with real ANSI codes when cfg.Colors is
// true, or all-empty strings when false. The values are hardcoded constants
// independent of the package-level Color* variables so that the result is
// solely determined by the RenderConfig.
func palette(cfg RenderConfig) colorPalette {
	if !cfg.Colors {
		return colorPalette{}
	}
	return colorPalette{
		red:       "\033[31m",
		green:     "\033[32m",
		yellow:    "\033[33m",
		cyan:      "\033[36m",
		magenta:   "\033[35m",
		dim:       "\033[2m",
		bold:      "\033[1m",
		boldRed:   "\033[1;31m",
		boldGreen: "\033[1;32m",
		reset:     "\033[0m",
	}
}

// activePalette returns a palette based on the current global ColorsEnabled
// state. Used by exported functions that maintain backward-compatible
// signatures (no RenderConfig parameter).
func activePalette() colorPalette {
	return palette(RenderConfig{Colors: ColorsEnabled})
}
