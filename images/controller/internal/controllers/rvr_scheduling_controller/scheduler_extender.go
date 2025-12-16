package rvr_scheduling_controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
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

type schedulerExtenderClient struct {
	httpClient *http.Client
	url        string
}

func (c *schedulerExtenderClient) filterNodesBySchedulerExtender(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	lvgToNodeNamesMap map[string][]string,
	log logr.Logger,
) (map[string]struct{}, error) {
	// Build LVG list from keys of the LVG->nodes map; thinPoolName is not known at this level.
	reqLVGs := make([]schedulerExtenderLVG, 0, len(lvgToNodeNamesMap))
	for name := range lvgToNodeNamesMap {
		reqLVGs = append(reqLVGs, schedulerExtenderLVG{Name: name})
	}

	if len(reqLVGs) == 0 {
		// No LVGs to check — which also implies no candidate nodes.
		return map[string]struct{}{}, nil
	}

	volType := "thick" // TODO get the real type from RSP
	size := rv.Spec.Size.Value()

	reqBody := schedulerExtenderRequest{
		LVGS: reqLVGs,
		Volume: schedulerExtenderVolume{
			Name: rv.Name,
			Size: size,
			Type: volType,
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		log.Error(err, "unable to marshal scheduler-extender request")
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		log.Error(err, "unable to build scheduler-extender request")
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Error(err, "scheduler-extender request failed")
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error(fmt.Errorf("unexpected status %d from scheduler-extender", resp.StatusCode), "scheduler-extender returned non-200 status")
		return nil, fmt.Errorf("scheduler-extender returned unexpected status %d", resp.StatusCode)
	}

	var respBody schedulerExtenderResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		log.Error(err, "unable to decode scheduler-extender response")
		return nil, err
	}

	if len(respBody.LVGS) == 0 {
		return map[string]struct{}{}, nil
	}

	allowedLVGs := make(map[string]struct{}, len(respBody.LVGS))
	for _, l := range respBody.LVGS {
		allowedLVGs[l.Name] = struct{}{}
	}

	nodesWithLVG := make(map[string]struct{})
	for lvgName, nodes := range lvgToNodeNamesMap {
		if _, ok := allowedLVGs[lvgName]; !ok {
			continue
		}
		for _, n := range nodes {
			nodesWithLVG[n] = struct{}{}
		}
	}

	if len(nodesWithLVG) == 0 {
		return map[string]struct{}{}, nil
	}

	return nodesWithLVG, nil
}
