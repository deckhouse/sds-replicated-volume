// Copyright 2025 Flant JSC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// generate_release_notes builds docs/RELEASE_NOTES.md and docs/RELEASE_NOTES.ru.md
// from YAML files in the CHANGELOG/ folder of the repository.
//
// It is a drop-in replacement for hack/generate_release_notes.py and produces
// byte-identical output for the inputs we currently use.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseVersionFromFilename strips the ".yml" or ".ru.yml" suffix from a
// changelog file name and returns the remaining version string.
func parseVersionFromFilename(filename string) string {
	base := strings.TrimSuffix(filename, ".ru.yml")
	base = strings.TrimSuffix(base, ".yml")
	return base
}

var leadingDigits = regexp.MustCompile(`^(\d+)`)

// versionKey extracts a numeric-component slice from a file path for sorting,
// mirroring the behaviour of packaging.version.parse for the version strings
// we use in changelogs (e.g. "v0.3.10" -> [0,3,10]).
func versionKey(path string) []int {
	name := filepath.Base(path)
	v := strings.TrimPrefix(parseVersionFromFilename(name), "v")
	parts := strings.Split(v, ".")
	keys := make([]int, 0, len(parts))
	for _, p := range parts {
		m := leadingDigits.FindStringSubmatch(p)
		if m == nil {
			keys = append(keys, 0)
			continue
		}
		n, _ := strconv.Atoi(m[1])
		keys = append(keys, n)
	}
	return keys
}

func compareVersionKeys(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(a) {
			ai = a[i]
		}
		if i < len(b) {
			bi = b[i]
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	return 0
}

func sortFilesByVersion(files []string) {
	sort.SliceStable(files, func(i, j int) bool {
		return compareVersionKeys(versionKey(files[i]), versionKey(files[j])) < 0
	})
}

func getChangelogFiles(dir string) (en, ru []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		full := filepath.Join(dir, name)
		switch {
		case strings.HasSuffix(name, ".ru.yml"):
			ru = append(ru, full)
		case strings.HasSuffix(name, ".yml"):
			en = append(en, full)
		}
	}
	sortFilesByVersion(en)
	sortFilesByVersion(ru)
	return en, ru, nil
}

// pyQuote emits a string the way Python's repr() does: prefers single quotes,
// switches to double quotes only when the string contains a single quote but
// no double quote.
func pyQuote(s string) string {
	hasSingle := strings.ContainsRune(s, '\'')
	hasDouble := strings.ContainsRune(s, '"')
	if hasSingle && !hasDouble {
		escaped := strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return `'` + escaped + `'`
}

// formatPyValue renders a YAML node in the same way Python's str() would
// stringify the corresponding object loaded by PyYAML.safe_load.
func formatPyValue(node *yaml.Node) string {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) > 0 {
			return formatPyValue(node.Content[0])
		}
		return "None"
	case yaml.ScalarNode:
		return pyQuote(node.Value)
	case yaml.MappingNode:
		parts := make([]string, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			parts = append(parts, formatPyValue(node.Content[i])+": "+formatPyValue(node.Content[i+1]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case yaml.SequenceNode:
		parts := make([]string, 0, len(node.Content))
		for _, c := range node.Content {
			parts = append(parts, formatPyValue(c))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case yaml.AliasNode:
		if node.Alias != nil {
			return formatPyValue(node.Alias)
		}
	}
	return node.Value
}

// formatChange formats one list item from the Changes/Изменения sequence.
// Plain scalars are emitted verbatim (no quotes), more complex nodes use the
// Python-style str() representation to stay compatible with the previous
// pyyaml-based script output.
func formatChange(node *yaml.Node) string {
	if node.Kind == yaml.ScalarNode {
		return node.Value
	}
	return formatPyValue(node)
}

func generateMarkdownContent(files []string, isRussian bool) string {
	var lines []string
	if isRussian {
		lines = append(lines, "---", `title: "Релизы"`, "---", "")
	} else {
		lines = append(lines, "---", `title: "Release Notes"`, "---", "")
	}

	changesKey := "Changes"
	if isRussian {
		changesKey = "Изменения"
	}

	for i := len(files) - 1; i >= 0; i-- {
		fp := files[i]
		version := parseVersionFromFilename(filepath.Base(fp))
		data, err := os.ReadFile(fp)
		if err != nil {
			fmt.Printf("Error loading file %s: %v\n", fp, err)
			continue
		}
		var root yaml.Node
		if err := yaml.Unmarshal(data, &root); err != nil {
			fmt.Printf("Error loading file %s: %v\n", fp, err)
			continue
		}
		if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
			continue
		}
		top := root.Content[0]
		if top.Kind != yaml.MappingNode {
			continue
		}

		var changes *yaml.Node
		for j := 0; j+1 < len(top.Content); j += 2 {
			if top.Content[j].Value == changesKey {
				changes = top.Content[j+1]
				break
			}
		}

		lines = append(lines, "## "+version, "")
		if changes != nil {
			if changes.Kind == yaml.SequenceNode {
				for _, c := range changes.Content {
					lines = append(lines, "* "+formatChange(c))
				}
			} else {
				lines = append(lines, "* "+formatChange(changes))
			}
		}
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func resolveProjectRoot() (string, error) {
	if root := os.Getenv("RELEASE_NOTES_ROOT"); root != "" {
		return root, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = resolved
	}
	return filepath.Dir(filepath.Dir(exe)), nil
}

func run() int {
	projectRoot, err := resolveProjectRoot()
	if err != nil {
		fmt.Println("Cannot determine project root:", err)
		return 1
	}
	changelogDir := filepath.Join(projectRoot, "CHANGELOG")
	outputDir := filepath.Join(projectRoot, "docs")

	fmt.Println("Working directory:", projectRoot)
	fmt.Println("Changelog folder:", changelogDir)
	fmt.Println("Output folder:", outputDir)

	if _, err := os.Stat(changelogDir); os.IsNotExist(err) {
		fmt.Printf("Error: folder %s not found\n", changelogDir)
		return 1
	}

	enFiles, ruFiles, err := getChangelogFiles(changelogDir)
	if err != nil {
		fmt.Println("Error reading changelog directory:", err)
		return 1
	}

	if len(enFiles) == 0 && len(ruFiles) == 0 {
		fmt.Println("No changelog files found")
		return 1
	}

	fmt.Printf("Found %d English files and %d Russian files\n", len(enFiles), len(ruFiles))

	if len(enFiles) != len(ruFiles) {
		fmt.Printf("Error: Number of English files (%d) does not match number of Russian files (%d)\n", len(enFiles), len(ruFiles))
		return 1
	}

	for _, name := range []string{"RELEASE_NOTES.md", "RELEASE_NOTES.ru.md"} {
		fp := filepath.Join(outputDir, name)
		if _, err := os.Stat(fp); err == nil {
			if err := os.Remove(fp); err != nil {
				fmt.Printf("Error removing file %s: %v\n", fp, err)
			} else {
				fmt.Printf("Removed existing file: %s\n", fp)
			}
		}
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		fmt.Println("Error creating output dir:", err)
		return 1
	}

	if len(enFiles) > 0 {
		content := generateMarkdownContent(enFiles, false)
		out := filepath.Join(outputDir, "RELEASE_NOTES.md")
		if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
			fmt.Printf("Error creating file %s: %v\n", out, err)
			return 1
		}
		fmt.Println("Created file:", out)
	}

	if len(ruFiles) > 0 {
		content := generateMarkdownContent(ruFiles, true)
		out := filepath.Join(outputDir, "RELEASE_NOTES.ru.md")
		if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
			fmt.Printf("Error creating file %s: %v\n", out, err)
			return 1
		}
		fmt.Println("Created file:", out)
	}

	fmt.Println("Release notes generation completed successfully!")
	return 0
}

func main() {
	os.Exit(run())
}
