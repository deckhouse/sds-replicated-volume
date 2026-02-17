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

// Package scheduler_extender provides a client for querying the scheduler-extender
// service for LVG capacity scores.
package scheduler_extender

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// Client queries the scheduler-extender for LVG capacity scores.
type Client interface {
	FilterAndScore(ctx context.Context, reservationID string, reservationTTL time.Duration, size int64, lvgs []LVMVolumeGroup) ([]ScoredLVMVolumeGroup, error)
	NarrowReservation(ctx context.Context, reservationID string, reservationTTL time.Duration, lvg LVMVolumeGroup) error
}

// LVMVolumeGroup represents a single LVM Volume Group query for the scheduler-extender.
type LVMVolumeGroup struct {
	LVGName      string `json:"name"`
	ThinPoolName string `json:"thinPoolName,omitempty"`
}

// ScoredLVMVolumeGroup is a LVMVolumeGroup with an assigned capacity score.
type ScoredLVMVolumeGroup struct {
	LVMVolumeGroup
	Score int `json:"score"`
}

type filterAndScoreRequest struct {
	ReservationID  string           `json:"reservationID"`
	ReservationTTL string           `json:"reservationTTL"`
	Size           int64            `json:"size"`
	LVGS           []LVMVolumeGroup `json:"lvgs"`
}

type response struct {
	LVGS []ScoredLVMVolumeGroup `json:"lvgs"`
}

type narrowReservationRequest struct {
	ReservationID  string         `json:"reservationID"`
	ReservationTTL string         `json:"reservationTTL"`
	LVG            LVMVolumeGroup `json:"lvg"`
}

type noOpClient struct{}

func NewNoOpClient() Client {
	return noOpClient{}
}

func (noOpClient) FilterAndScore(_ context.Context, _ string, _ time.Duration, _ int64, lvgs []LVMVolumeGroup) ([]ScoredLVMVolumeGroup, error) {
	scored := make([]ScoredLVMVolumeGroup, len(lvgs))
	for i, lvg := range lvgs {
		scored[i] = ScoredLVMVolumeGroup{LVMVolumeGroup: lvg, Score: 0}
	}
	return scored, nil
}

func (noOpClient) NarrowReservation(context.Context, string, time.Duration, LVMVolumeGroup) error {
	return nil
}

type httpClient struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient creates a new scheduler-extender client for the given base URL.
// Endpoints: baseURL + "/v1/lvg/filter-and-score" and baseURL + "/v1/lvg/narrow-reservation".
func NewClient(baseURL string) (Client, error) {
	if baseURL == "" {
		return nil, errors.New("scheduler-extender URL is not configured")
	}

	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invalid scheduler-extender URL: %w", err)
	}

	// TODO: configure proper TLS verification. Currently skipped because the
	// scheduler-extender is an internal cluster service accessed via in-cluster networking.
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see TODO above
	}
	hc := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	return &httpClient{
		httpClient: hc,
		baseURL:    baseURL,
	}, nil
}

func (c *httpClient) FilterAndScore(
	ctx context.Context,
	reservationID string,
	reservationTTL time.Duration,
	size int64,
	lvgs []LVMVolumeGroup,
) ([]ScoredLVMVolumeGroup, error) {
	if len(lvgs) == 0 {
		return nil, fmt.Errorf("no LVGs provided for query")
	}

	reqBody := filterAndScoreRequest{
		ReservationID:  reservationID,
		ReservationTTL: reservationTTL.String(),
		Size:           size,
		LVGS:           lvgs,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal scheduler-extender filter-and-score request: %w", err)
	}

	endpoint := c.baseURL + "/v1/lvg/filter-and-score"
	logger := crlog.FromContext(ctx).WithName("scheduler-extender")
	logger.V(3).Info("filter-and-score request",
		"endpoint", endpoint,
		"reservationID", reservationID,
		"reservationTTL", reservationTTL.String(),
		"size", size,
		"lvgCount", len(lvgs),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unable to build scheduler-extender filter-and-score request: %w", err)
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

	var respBody response
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return nil, fmt.Errorf("unable to decode scheduler-extender response: %w", err)
	}

	logger.V(3).Info("filter-and-score response",
		"endpoint", endpoint,
		"reservationID", reservationID,
		"scoredCount", len(respBody.LVGS),
	)

	return respBody.LVGS, nil
}

func (c *httpClient) NarrowReservation(
	ctx context.Context,
	reservationID string,
	reservationTTL time.Duration,
	lvg LVMVolumeGroup,
) error {
	reqBody := narrowReservationRequest{
		ReservationID:  reservationID,
		ReservationTTL: reservationTTL.String(),
		LVG:            lvg,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("unable to marshal scheduler-extender narrow-reservation request: %w", err)
	}

	endpoint := c.baseURL + "/v1/lvg/narrow-reservation"
	logger := crlog.FromContext(ctx).WithName("scheduler-extender")
	logger.V(3).Info("narrow-reservation request",
		"endpoint", endpoint,
		"reservationID", reservationID,
		"reservationTTL", reservationTTL.String(),
		"lvg", lvg.LVGName,
		"thinPool", lvg.ThinPoolName,
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("unable to build scheduler-extender narrow-reservation request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("scheduler-extender narrow-reservation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("scheduler-extender narrow-reservation returned unexpected status %d", resp.StatusCode)
	}

	logger.V(3).Info("narrow-reservation response",
		"endpoint", endpoint,
		"reservationID", reservationID,
		"status", resp.StatusCode,
	)

	return nil
}
