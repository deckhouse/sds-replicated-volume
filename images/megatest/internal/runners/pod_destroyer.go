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
	"log/slog"
	"math/rand"

	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/config"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

// PodDestroyer periodically deletes random control-plane pods by label selector
// It does NOT wait for deletion to succeed
type PodDestroyer struct {
	cfg    config.PodDestroyerConfig
	client *kubeutils.Client
	log    *slog.Logger
}

// NewPodDestroyer creates a new PodDestroyer
func NewPodDestroyer(
	cfg config.PodDestroyerConfig,
	client *kubeutils.Client,
) *PodDestroyer {
	return &PodDestroyer{
		cfg:    cfg,
		client: client,
		log: slog.Default().With(
			"runner", "pod-destroyer",
			"namespace", cfg.Namespace,
			"label_selector", cfg.LabelSelector,
		),
	}
}

// Run starts the destroy cycle until context is cancelled
func (p *PodDestroyer) Run(ctx context.Context) error {
	p.log.Info("started")
	defer p.log.Info("finished")

	for {
		// Wait random duration before delete
		if err := waitRandomWithContext(ctx, p.cfg.Period); err != nil {
			return nil
		}

		// Perform delete
		if err := p.doDestroy(ctx); err != nil {
			p.log.Error("destroy failed", "error", err)
			// Continue even on failure
		}
	}
}

func (p *PodDestroyer) doDestroy(ctx context.Context) error {
	// Get list of pods
	pods, err := p.client.ListPods(ctx, p.cfg.Namespace, p.cfg.LabelSelector)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		p.log.Debug("no pods found to delete")
		return nil
	}

	// Shuffle the list
	//nolint:gosec // G404: math/rand is fine for non-security-critical random selection
	rand.Shuffle(len(pods), func(i, j int) {
		pods[i], pods[j] = pods[j], pods[i]
	})

	// Determine how many to delete
	toDelete := randomInt(p.cfg.PodCount.Min, p.cfg.PodCount.Max)
	if toDelete > len(pods) {
		toDelete = len(pods)
	}

	p.log.Debug("deleting pods", "total_pods", len(pods), "to_delete", toDelete)

	// Delete pods
	deleted := 0
	for i := 0; i < len(pods) && deleted < toDelete; i++ {
		pod := &pods[i]

		p.log.Info("pod delete initiated",
			"pod_name", pod.Name,
			"namespace", pod.Namespace,
			"action", "delete",
		)

		if err := p.client.DeletePod(ctx, pod); err != nil {
			p.log.Error("failed to delete pod",
				"pod_name", pod.Name,
				"namespace", pod.Namespace,
				"error", err,
			)
			// Continue with other pods even on failure
		}
		deleted++
	}

	return nil
}
