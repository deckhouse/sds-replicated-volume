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

package runners

import (
	"context"
	"math/rand"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/k8sclient"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/logging"
)

// PodDestroyer periodically deletes random pods matching a label selector
type PodDestroyer struct {
	cfg      config.PodDestroyerConfig
	client   *k8sclient.Client
	log      *logging.Logger
	selector labels.Selector
}

// NewPodDestroyer creates a new PodDestroyer
func NewPodDestroyer(
	cfg config.PodDestroyerConfig,
	client *k8sclient.Client,
) (*PodDestroyer, error) {
	selector, err := labels.Parse(cfg.LabelSelector)
	if err != nil {
		return nil, err
	}

	return &PodDestroyer{
		cfg:      cfg,
		client:   client,
		log:      logging.GlobalLogger("pod-destroyer"),
		selector: selector,
	}, nil
}

// Name returns the runner name
func (p *PodDestroyer) Name() string {
	return "pod-destroyer"
}

// Run starts the destroy cycle until context is cancelled
func (p *PodDestroyer) Run(ctx context.Context) error {
	p.log.Info("starting pod destroyer",
		"namespace", p.cfg.Namespace,
		"selector", p.cfg.LabelSelector,
	)
	defer p.log.Info("pod destroyer stopped")

	for {
		// Wait random duration before delete
		if err := waitRandomWithContext(ctx, p.cfg.Period); err != nil {
			return nil
		}

		// Perform delete
		if err := p.doDestroy(ctx); err != nil {
			p.log.Error("destroy failed", err)
			// Continue even on failure
		}
	}
}

func (p *PodDestroyer) doDestroy(ctx context.Context) error {
	// List pods matching selector
	pods, err := p.client.ListPods(ctx, p.cfg.Namespace, p.selector)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		p.log.Info("no pods to destroy")
		return nil
	}

	// Determine how many pods to delete
	count := randomInt(p.cfg.PodCount.Min, p.cfg.PodCount.Max)
	if count > len(pods) {
		count = len(pods)
	}

	// Shuffle and take first count pods
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(pods), func(i, j int) {
		pods[i], pods[j] = pods[j], pods[i]
	})
	selectedPods := pods[:count]

	// Delete each selected pod
	for _, pod := range selectedPods {
		params := logging.ActionParams{
			"namespace": p.cfg.Namespace,
			"pod_name":  pod.Name,
			"node":      pod.Spec.NodeName,
		}

		p.log.ActionStarted("destroy_pod", params)
		startTime := time.Now()

		err := p.client.DeletePod(ctx, p.cfg.Namespace, pod.Name)
		if err != nil {
			p.log.ActionFailed("destroy_pod", params, err, time.Since(startTime))
			// Continue deleting other pods
			continue
		}

		// Don't wait for deletion to complete
		p.log.ActionCompleted("destroy_pod", params, "delete_initiated", time.Since(startTime))
	}

	return nil
}
