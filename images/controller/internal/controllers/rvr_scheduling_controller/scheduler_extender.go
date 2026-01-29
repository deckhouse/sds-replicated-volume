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

package rvrschedulingcontroller

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

type SchedulerExtenderClient interface {
	QueryLVGScores(ctx context.Context, lvgs []LVGQuery, volume VolumeInfo) (map[string]int, error)
}

type LVGQuery struct {
	Name         string `json:"name"`
	ThinPoolName string `json:"thinPoolName,omitempty"`
}

type VolumeInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type"`
}

type schedulerExtenderRequest struct {
	LVGS   []LVGQuery `json:"lvgs"`
	Volume VolumeInfo `json:"volume"`
}

type schedulerExtenderResponseLVG struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
}

type schedulerExtenderResponse struct {
	LVGS []schedulerExtenderResponseLVG `json:"lvgs"`
}

type httpSchedulerExtenderClient struct {
	httpClient *http.Client
	url        string
}

func NewSchedulerExtenderClient() (SchedulerExtenderClient, error) {
	extURL := os.Getenv("SCHEDULER_EXTENDER_URL")
	if extURL == "" {
		return nil, errors.New("scheduler-extender URL is not configured")
	}

	_, err := url.Parse(extURL)
	if err != nil {
		return nil, fmt.Errorf("invalid scheduler-extender URL: %w", err)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient := &http.Client{Transport: tr}

	return &httpSchedulerExtenderClient{
		httpClient: httpClient,
		url:        extURL,
	}, nil
}

func (c *httpSchedulerExtenderClient) QueryLVGScores(
	ctx context.Context,
	lvgs []LVGQuery,
	volumeInfo VolumeInfo,
) (map[string]int, error) {
	if len(lvgs) == 0 {
		return nil, fmt.Errorf("no LVGs provided for query")
	}

	reqBody := schedulerExtenderRequest{
		LVGS:   lvgs,
		Volume: volumeInfo,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal scheduler-extender request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unable to build scheduler-extender request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("scheduler-extender request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scheduler-extender returned unexpected status %d", resp.StatusCode)
	}

	var respBody schedulerExtenderResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return nil, fmt.Errorf("unable to decode scheduler-extender response: %w", err)
	}

	lvgScores := make(map[string]int, len(respBody.LVGS))
	for _, lvg := range respBody.LVGS {
		lvgScores[lvg.Name] = lvg.Score
	}

	return lvgScores, nil
}
