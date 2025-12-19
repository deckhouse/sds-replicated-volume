/*
Copyright 2025 Flant JSC

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

package rvr_scheduling_controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
)

type schedulerExtenderLVG struct {
	Name         string `json:"name"`
	ThinPoolName string `json:"thinPoolName,omitempty"`
}

type schedulerExtenderVolume struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type"`
}

type schedulerExtenderRequest struct {
	LVGS   []schedulerExtenderLVG  `json:"lvgs"`
	Volume schedulerExtenderVolume `json:"volume"`
}

type schedulerExtenderResponseLVG struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
}

type schedulerExtenderResponse struct {
	LVGS []schedulerExtenderResponseLVG `json:"lvgs"`
}

type SchedulerExtenderClient struct {
	httpClient *http.Client
	url        string
}

func NewSchedulerHTTPClient() (*SchedulerExtenderClient, error) {
	extURL := os.Getenv("SCHEDULER_EXTENDER_URL") // TODO init in the other place later
	if extURL == "" {
		// No scheduler-extender URL configured — disable external capacity filtering.
		return nil, errors.New("scheduler-extender URL is not configured")
	}
	return &SchedulerExtenderClient{
		httpClient: http.DefaultClient,
		url:        extURL,
	}, nil
}

func (c *SchedulerExtenderClient) filterNodesBySchedulerExtender(
	ctx context.Context,
	sctx *SchedulingContext,
) error {
	// Collect all candidate nodes from ZonesToNodeCandidatesMap
	candidateNodes := make(map[string]struct{})
	for _, candidates := range sctx.ZonesToNodeCandidatesMap {
		for _, candidate := range candidates {
			candidateNodes[candidate.Name] = struct{}{}
		}
	}

	// Build LVG list from SpLvgToNodeInfoMap, but only for nodes in ZonesToNodeCandidatesMap
	reqLVGs := make([]schedulerExtenderLVG, 0, len(sctx.RspLvgToNodeInfoMap))
	for lvgName, info := range sctx.RspLvgToNodeInfoMap {
		// Skip LVGs whose nodes are not in the candidate list
		if _, ok := candidateNodes[info.NodeName]; !ok {
			continue
		}
		reqLVGs = append(reqLVGs, schedulerExtenderLVG{
			Name:         lvgName,
			ThinPoolName: info.ThinPoolName,
		})
	}

	if len(reqLVGs) == 0 {
		// No LVGs to check — no candidate nodes have LVGs from the storage pool
		return fmt.Errorf("no candidate nodes have LVGs from storage pool %s", sctx.Rsc.Spec.StoragePool)
	}

	var volType string
	switch sctx.Rsp.Spec.Type {
	case "LVMThin":
		volType = "thin"
	case "LVM":
		volType = "thick"
	default:
		return fmt.Errorf("RSP volume type is not supported: %s", sctx.Rsp.Spec.Type)
	}
	size := sctx.Rv.Spec.Size.Value()

	reqBody := schedulerExtenderRequest{
		LVGS: reqLVGs,
		Volume: schedulerExtenderVolume{
			Name: sctx.Rv.Name,
			Size: size,
			Type: volType,
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		sctx.Log.Error(err, "unable to marshal scheduler-extender request")
		return fmt.Errorf("unable to marshal scheduler-extender request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		sctx.Log.Error(err, "unable to build scheduler-extender request")
		return fmt.Errorf("unable to build scheduler-extender request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		sctx.Log.Error(err, "scheduler-extender request failed")
		return fmt.Errorf("scheduler-extender request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		sctx.Log.Error(nil, "scheduler-extender returned non-200 status", "status", resp.StatusCode)
		return fmt.Errorf("scheduler-extender returned unexpected status %d", resp.StatusCode)
	}

	var respBody schedulerExtenderResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		sctx.Log.Error(err, "unable to decode scheduler-extender response")
		return fmt.Errorf("unable to decode scheduler-extender response: %w", err)
	}

	// Build map of LVG name -> score from response
	lvgScores := make(map[string]int, len(respBody.LVGS))
	for _, lvg := range respBody.LVGS {
		lvgScores[lvg.Name] = lvg.Score
	}

	// Build map of node -> score based on LVG scores
	// Node gets the score of its LVG (if LVG is in the response)
	nodeScores := make(map[string]int)
	for lvgName, info := range sctx.RspLvgToNodeInfoMap {
		if score, ok := lvgScores[lvgName]; ok {
			nodeScores[info.NodeName] = score
		}
	}

	// Filter ZonesToNodeCandidatesMap: keep only nodes that have score (i.e., their LVG was returned)
	// and update their scores
	for zone, candidates := range sctx.ZonesToNodeCandidatesMap {
		filteredCandidates := make([]NodeCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if score, ok := nodeScores[candidate.Name]; ok {
				filteredCandidates = append(filteredCandidates, NodeCandidate{
					Name:  candidate.Name,
					Score: score,
				})
			}
			// Node not in response — skip (no capacity)
		}
		if len(filteredCandidates) > 0 {
			sctx.ZonesToNodeCandidatesMap[zone] = filteredCandidates
		} else {
			delete(sctx.ZonesToNodeCandidatesMap, zone)
		}
	}

	return nil
}
