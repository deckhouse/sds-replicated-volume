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

// Package linstorviewer renders LINSTOR CLI-style listings from crs.gz backups.
package linstorviewer

import (
	"fmt"
	"os"
	"strings"

	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstorviewer/index"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstorviewer/load"
	"github.com/deckhouse/sds-replicated-volume/images/linstor-migrator/internal/linstorviewer/render"
)

const (
	cmdNodeList        = "node list"
	cmdStoragePoolList = "storage-pool list"
	cmdVolumeList      = "volume list"
)

// Run executes the linstor backup viewer CLI. args is os.Args[1:].
func Run(args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		printUsage(os.Stdout)
		return 0
	}

	if len(args) == 1 {
		if isHelp(args[0]) {
			printUsage(os.Stdout)
			return 0
		}
		fmt.Fprintf(os.Stderr, "missing command after backup file\n\n")
		printUsage(os.Stderr)
		return 2
	}

	crsPath := args[0]
	if isHelp(crsPath) {
		printUsage(os.Stdout)
		return 0
	}

	cmdArgs := args[1:]
	for _, a := range cmdArgs {
		if isHelp(a) {
			printUsage(os.Stdout)
			return 0
		}
	}

	cmd := strings.Join(cmdArgs, " ")
	docs, err := load.FromFile(crsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	store, err := index.Build(docs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	var out string
	switch cmd {
	case cmdNodeList:
		out = render.NodeList(store)
	case cmdStoragePoolList:
		out = render.StoragePoolList(store)
	case cmdVolumeList:
		out = render.VolumeList(store)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printUsage(os.Stderr)
		return 2
	}

	fmt.Print(out)
	return 0
}

func isHelp(s string) bool {
	return s == "-h" || s == "--help" || s == "-help"
}

func printUsage(w *os.File) {
	_, _ = fmt.Fprintf(w, `linstor-viewer — read-only LINSTOR listings from a migrator crs.gz backup

Usage:
  linstor-viewer [-h|--help]
  linstor-viewer <crs.gz> [-h|--help]
  linstor-viewer <crs.gz> node list
  linstor-viewer <crs.gz> storage-pool list
  linstor-viewer <crs.gz> volume list

The backup is a snapshot of LINSTOR Kubernetes CRs. Fields not stored in CRs are shown as "-".

Examples:
  linstor-viewer crs.gz node list
  linstor-viewer crs.gz storage-pool list
  linstor-viewer crs.gz volume list
`)
}
