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

package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	srvv1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var (
	// ErrScenarioStateNotFound indicates that no snapshot file was found for runID.
	ErrScenarioStateNotFound = errors.New("migrator scenario state file not found")
	// ErrScenarioStateAmbiguous indicates that multiple snapshot files were found for runID.
	ErrScenarioStateAmbiguous = errors.New("multiple migrator scenario state files found for runID")
)

// RSPBaseline stores old control-plane RSP name and spec for comparison.
type RSPBaseline struct {
	Name string                          `json:"name"`
	Spec srvv1.ReplicatedStoragePoolSpec `json:"spec"`
}

// MigratorScenarioState stores all data required to re-run post-migration checks.
type MigratorScenarioState struct {
	Timestamp     string            `json:"timestamp"`
	RunID         string            `json:"run_id"`
	OldRSPs       []RSPBaseline     `json:"old_rsps"`
	PodNames      []string          `json:"pod_names"`
	FileChecksums map[string]string `json:"file_checksums"`
	LinstorBefore *LinstorState     `json:"linstor_before"`

	MigratedResources []string          `json:"migrated_resources"`
	PVToRSC           map[string]string `json:"pv_to_rsc"`
}

// SaveMigratorScenarioState saves the full scenario snapshot to /tmp.
func SaveMigratorScenarioState(state *MigratorScenarioState) (string, error) {
	if state == nil {
		return "", fmt.Errorf("scenario state is nil")
	}
	if strings.TrimSpace(state.RunID) == "" {
		return "", fmt.Errorf("scenario state runID is empty")
	}
	if state.LinstorBefore == nil {
		return "", fmt.Errorf("scenario state linstor_before is nil")
	}

	if state.Timestamp == "" {
		state.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if state.FileChecksums == nil {
		state.FileChecksums = map[string]string{}
	}

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal scenario state: %w", err)
	}

	safeRunID := strings.ReplaceAll(state.RunID, "/", "_")
	filePath := fmt.Sprintf("%s%s-%d.json", linstorStateFilePrefix, safeRunID, time.Now().UTC().Unix())
	if err := os.WriteFile(filePath, payload, 0o644); err != nil {
		return "", fmt.Errorf("failed to write scenario state file %s: %w", filePath, err)
	}

	return filePath, nil
}

// RestoreMigratorScenarioState loads scenario snapshot by runID from /tmp.
func RestoreMigratorScenarioState(runID string) (*MigratorScenarioState, string, error) {
	safeRunID := strings.ReplaceAll(strings.TrimSpace(runID), "/", "_")
	if safeRunID == "" {
		return nil, "", fmt.Errorf("runID is empty")
	}

	pattern := fmt.Sprintf("%s%s-*.json", linstorStateFilePrefix, safeRunID)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, "", fmt.Errorf("failed to search state files by pattern %q: %w", pattern, err)
	}
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("%w: runID=%s", ErrScenarioStateNotFound, safeRunID)
	}
	if len(matches) > 1 {
		return nil, "", fmt.Errorf("%w: runID=%s matched %d files (%v)", ErrScenarioStateAmbiguous, safeRunID, len(matches), matches)
	}

	filePath := matches[0]
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read scenario state file %s: %w", filePath, err)
	}

	var state MigratorScenarioState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, "", fmt.Errorf("failed to parse scenario state file %s: %w", filePath, err)
	}
	if strings.TrimSpace(state.RunID) == "" {
		return nil, "", fmt.Errorf("scenario state file %s has empty run_id", filePath)
	}
	if state.LinstorBefore == nil {
		return nil, "", fmt.Errorf("scenario state file %s has empty linstor_before", filePath)
	}
	if state.FileChecksums == nil {
		state.FileChecksums = map[string]string{}
	}

	return &state, filePath, nil
}
