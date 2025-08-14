/*
Copyright 2022 Flant JSC

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

package manualcertrenewal

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/utils"
)

const (
	TriggerKeyStep                     = "step"
	TriggerKeyBackupDaemonSetAffinity  = "backup-daemonset-affinity-"
	TriggerKeyBackupDeploymentReplicas = "backup-deployment-replicas-"

	DaemonSetNameCsiNode = "csi-node"
	DaemonSetNameNode    = "linstor-node"

	DeploymentNameSchedulerExtender  = "linstor-scheduler-extender"
	DeploymentNameWebhooks           = "webhooks"
	DeploymentNameSpaas              = "spaas"
	DeploymentNameController         = "linstor-controller"
	DeploymentNameCsiController      = "linstor-csi-controller"
	DeploymentNameAffinityController = "linstor-affinity-controller"
	DeploymentNameSdsRVController    = "sds-replicated-volume-controller"

	WaitForResourcesPollInterval = 2 * time.Second
)

var (
	DaemonSetNameList = []string{DaemonSetNameCsiNode, DaemonSetNameNode}

	DeploymentNameList = []string{
		DeploymentNameSchedulerExtender,
		DeploymentNameWebhooks,
		DeploymentNameSpaas,
		DeploymentNameAffinityController,
		DeploymentNameController,
		DeploymentNameCsiController,
		DeploymentNameSdsRVController,
	}
)

type step struct {
	Name    string
	Execute func() error
}

func (s step) String() string { return s.Name }

type stateMachine struct {
	ctx     context.Context
	trigger *v1.ConfigMap
	cl      client.Client
	log     pkg.Logger

	currentStepIdx int
	steps          []step

	cachedSecrets     map[string]*v1.Secret
	cachedDaemonSets  map[string]*appsv1.DaemonSet
	cachedDeployments map[string]*appsv1.Deployment

	hookInput *pkg.HookInput
}

func newStateMachine(
	ctx context.Context,
	cl client.Client,
	log pkg.Logger,
	trigger *v1.ConfigMap,
	hookInput *pkg.HookInput,
) *stateMachine {
	s := &stateMachine{}

	steps := []step{
		{
			Name:    "Prepared",
			Execute: s.prepare,
		},
		{
			Name:    "TurnedOffAndRenewedCerts",
			Execute: s.turnOffAndRenewCerts,
		},
		{
			Name:    "TurnedOn",
			Execute: s.turnOn,
		},
		{
			Name:    "Done",
			Execute: s.moveToDone,
		},
	}

	// determine current step
	stepName := trigger.Data[TriggerKeyStep]
	currentStepIdx := slices.IndexFunc(
		steps,
		func(s step) bool { return s.Name == stepName },
	)

	s.ctx = ctx
	s.cl = cl
	s.log = log
	s.trigger = trigger
	s.currentStepIdx = currentStepIdx
	s.steps = steps
	s.hookInput = hookInput

	return s
}

func (s *stateMachine) run() error {
	for {
		nextStep, ok := s.nextStep()
		if !ok {
			if _, hasLabel := s.trigger.Labels[ConfigMapCompletedLabel]; hasLabel {
				s.log.Debug("completion label found, stay in Done")
				break
			}
			// reset to start
			s.log.Info("completion label not found, resetting")
			if err := s.reset(); err != nil {
				return fmt.Errorf("resetting trigger: %w", err)
			}
			continue
		}

		s.log.Info("executing step", "step", nextStep.Name)

		if err := nextStep.Execute(); err != nil {
			return fmt.Errorf("executing step %s: %w", nextStep, err)
		}

		if err := s.updateTrigger(nextStep); err != nil {
			return fmt.Errorf("updating trigger to '%s': %w", nextStep, err)
		}

		s.currentStepIdx++
	}

	s.log.Debug("no more steps to execute")
	return nil
}

func (s *stateMachine) nextStep() (step, bool) {
	if s.currentStepIdx < len(s.steps)-1 {
		// go to next step
		return s.steps[s.currentStepIdx+1], true
	}
	// stay on the same step, and report we're done
	return s.steps[s.currentStepIdx], false
}

func (s *stateMachine) reset() error {
	s.currentStepIdx = -1
	s.trigger.Data = map[string]string{}
	return s.cl.Update(s.ctx, s.trigger)
}

func (s *stateMachine) updateTrigger(step step) error {
	utils.MapEnsureAndSet(&s.trigger.Data, TriggerKeyStep, step.Name)
	return s.cl.Update(s.ctx, s.trigger)
}

func (s *stateMachine) prepare() error {
	if err := s.checkResourcesHealth(); err != nil {
		return fmt.Errorf("checking resources: %w", err)
	}

	// prevent hooks from reacting to our changes
	for conf := range allCertConfigs() {
		secret, err := s.getSecret(conf.TLSSecretName, false)
		if err != nil {
			return err
		}

		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[consts.SecretCertHookSuppressedByLabel] = PackageName

		if err := s.cl.Update(s.ctx, secret); err != nil {
			return fmt.Errorf("updating secret %s: %w", secret.Name, err)
		}
	}

	// add finalizer
	s.trigger.SetFinalizers([]string{PackageURI})

	// prevent stale events from own updates
	utils.MapEnsureAndSet(&s.trigger.Labels, ConfigMapInProgressLabel, "true")

	// backup
	for _, name := range DaemonSetNameList {
		if err := s.backupDaemonSet(name); err != nil {
			return fmt.Errorf("creating backup for daemonset %s: %w", name, err)
		}
	}

	for _, name := range DeploymentNameList {
		if err := s.backupDeployment(name); err != nil {
			return fmt.Errorf("creating backup for deployment %s: %w", name, err)
		}
	}

	return nil
}

func (s *stateMachine) checkResourcesHealth() error {
	for _, name := range DaemonSetNameList {
		if err := s.waitForDaemonSetReady(name); err != nil {
			return fmt.Errorf("waiting daemonset %s to be ready: %w", name, err)
		}
	}

	for _, name := range DeploymentNameList {
		if err := s.waitForDeploymentReady(name); err != nil {
			return fmt.Errorf("waiting deployment %s to be ready: %w", name, err)
		}
	}

	return nil
}

func (s *stateMachine) backupDeployment(name string) error {
	depl, err := s.getDeployment(name, false)
	if err != nil {
		return err
	}

	replicasJSON, err := json.Marshal(depl.Spec.Replicas)
	if err != nil {
		return fmt.Errorf("marshalling deployment.spec.replicas for backup: %w", err)
	}
	utils.MapEnsureAndSet(
		&s.trigger.Data,
		TriggerKeyBackupDeploymentReplicas+name,
		string(replicasJSON),
	)

	return nil
}

func (s *stateMachine) backupDaemonSet(name string) error {
	ds, err := s.getDaemonSet(name, false)
	if err != nil {
		return err
	}

	affinityJSON, err := json.Marshal(ds.Spec.Template.Spec.Affinity)
	if err != nil {
		return fmt.Errorf("marshalling daemonset.template.spec.affinity for backup: %w", err)
	}
	utils.MapEnsureAndSet(
		&s.trigger.Data,
		TriggerKeyBackupDaemonSetAffinity+name,
		string(affinityJSON),
	)

	return nil
}

func (s *stateMachine) turnOffAndRenewCerts() error {
	// order of shutdown is important
	if err := s.turnOffDaemonSetAndWait(DaemonSetNameCsiNode); err != nil {
		return err
	}

	if err := s.turnOffDeploymentAndWait(DeploymentNameCsiController); err != nil {
		return err
	}

	if err := s.turnOffDeploymentAndWait(DeploymentNameSchedulerExtender); err != nil {
		return err
	}

	if err := s.turnOffDeploymentAndWait(DeploymentNameSpaas); err != nil {
		return err
	}

	if err := s.turnOffDeploymentAndWait(DeploymentNameWebhooks); err != nil {
		return err
	}

	if err := s.turnOffDeploymentAndWait(DeploymentNameAffinityController); err != nil {
		return err
	}

	if err := s.turnOffDaemonSetAndWait(DaemonSetNameNode); err != nil {
		return err
	}

	if err := s.turnOffDeploymentAndWait(DeploymentNameController); err != nil {
		return err
	}

	if err := s.turnOffDeploymentAndWait(DeploymentNameSdsRVController); err != nil {
		return err
	}

	if err := s.renewCerts(); err != nil {
		return err
	}

	return nil
}

func (s *stateMachine) turnOffDaemonSetAndWait(name string) error {
	ds, err := s.getDaemonSet(name, false)
	if err != nil {
		return err
	}

	// turn off
	patch := client.MergeFrom(ds.DeepCopy())
	ds.Spec.Template.Spec.Affinity = &v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{}, // match no objects
				},
			},
		},
	}
	if err := s.cl.Patch(s.ctx, ds, patch); err != nil {
		return fmt.Errorf("patching daemonset %s: %w", name, err)
	}

	// wait
	if err := s.waitForAppPodsDeleted(name); err != nil {
		return fmt.Errorf(
			"waiting for daemonset '%s' pods to be deleted: %w",
			name,
			err,
		)
	}

	return nil
}

func (s *stateMachine) turnOffDeploymentAndWait(name string) error {
	s.log.Info("turning off deployment", "name", name)

	depl, err := s.getDeployment(name, false)
	if err != nil {
		return err
	}

	// backup
	replicasJSON, err := json.Marshal(depl.Spec.Replicas)
	if err != nil {
		return fmt.Errorf("marshalling deployment.spec.replicas for backup: %w", err)
	}
	utils.MapEnsureAndSet(
		&s.trigger.Data,
		TriggerKeyBackupDeploymentReplicas+name,
		string(replicasJSON),
	)

	// turn off
	patch := client.MergeFrom(depl.DeepCopy())

	depl.Spec.Replicas = new(int32)

	if err := s.cl.Patch(s.ctx, depl, patch); err != nil {
		return fmt.Errorf("patching deployment %s: %w", name, err)
	}

	// wait
	if err := s.waitForAppPodsDeleted(name); err != nil {
		return fmt.Errorf(
			"waiting for deployment '%s' pods to be deleted: %w",
			name,
			err,
		)
	}

	return nil
}

func (s *stateMachine) turnOn() error {
	if err := s.turnOnDeploymentAndWait(DeploymentNameSdsRVController); err != nil {
		return err
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameController); err != nil {
		return err
	}

	if err := s.turnOnDaemonSetAndWait(DaemonSetNameNode); err != nil {
		return err
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameAffinityController); err != nil {
		return err
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameWebhooks); err != nil {
		return err
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameSpaas); err != nil {
		return err
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameSchedulerExtender); err != nil {
		return err
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameCsiController); err != nil {
		return err
	}

	if err := s.turnOnDaemonSetAndWait(DaemonSetNameCsiNode); err != nil {
		return err
	}

	return nil
}

func (s *stateMachine) turnOnDaemonSetAndWait(name string) error {
	s.log.Info("turnOnDaemonSetAndWait", "name", name)

	ds, err := s.getDaemonSet(name, true)
	if err != nil {
		return err
	}

	originalAffinityJSON := s.trigger.Data[TriggerKeyBackupDaemonSetAffinity+name]

	originalAffinity := &corev1.Affinity{}
	if err := json.Unmarshal([]byte(originalAffinityJSON), originalAffinity); err != nil {
		return fmt.Errorf("unmarshalling original affinity to restore %s: %w", name, err)
	}

	patch := client.MergeFrom(ds.DeepCopy())

	ds.Spec.Template.Spec.Affinity = originalAffinity

	if err := s.cl.Patch(s.ctx, ds, patch); err != nil {
		return fmt.Errorf("patching daemonset %s: %w", name, err)
	}

	// wait
	if err := s.waitForDaemonSetReady(name); err != nil {
		return fmt.Errorf(
			"waiting for daemonset '%s' pods to be deleted: %w",
			name,
			err,
		)
	}
	return nil
}

func (s *stateMachine) turnOnDeploymentAndWait(name string) error {
	s.log.Info("turnOnDeploymentAndWait", "name", name)

	depl, err := s.getDeployment(name, true)
	if err != nil {
		return err
	}

	// restore original value
	replicasJSON := s.trigger.Data[TriggerKeyBackupDeploymentReplicas+name]

	var originalReplicas = new(int32)
	if err := json.Unmarshal([]byte(replicasJSON), originalReplicas); err != nil {
		return fmt.Errorf("unmarshalling original replicas to restore %s: %w", name, err)
	}

	patch := client.MergeFrom(depl.DeepCopy())

	depl.Spec.Replicas = originalReplicas

	if err := s.cl.Patch(s.ctx, depl, patch); err != nil {
		return fmt.Errorf("patching deployment %s: %w", name, err)
	}

	// wait
	if err := s.waitForDeploymentReady(name); err != nil {
		return fmt.Errorf(
			"waiting for deployment '%s' replicas to become available: %w",
			name,
			err,
		)
	}
	return nil
}

func (s *stateMachine) waitForDaemonSetReady(name string) error {
	s.log.Info("waitForDaemonSetReady", "name", name)

	return wait.PollUntilContextCancel(
		s.ctx,
		WaitForResourcesPollInterval,
		true,
		func(_ context.Context) (bool, error) {
			ds, err := s.getDaemonSet(name, true)
			if err != nil {
				return false, err
			}

			desired := ds.Status.DesiredNumberScheduled
			if desired != ds.Status.CurrentNumberScheduled ||
				desired != ds.Status.UpdatedNumberScheduled ||
				desired != ds.Status.NumberAvailable {
				s.log.Info(
					"daemonset is not ready yet",
					"name", name,
					"status.desiredNumberScheduled", desired,
					"status.currentNumberScheduled", ds.Status.CurrentNumberScheduled,
					"status.updatedNumberScheduled", ds.Status.UpdatedNumberScheduled,
					"status.numberAvailable", ds.Status.NumberAvailable,
				)
				return false, nil
			}

			return true, nil
		},
	)
}

func (s *stateMachine) waitForDeploymentReady(name string) error {
	s.log.Info("waitForDeploymentReady", "name", name)

	return wait.PollUntilContextCancel(
		s.ctx,
		WaitForResourcesPollInterval,
		true,
		func(_ context.Context) (bool, error) {
			dep, err := s.getDeployment(name, true)
			if err != nil {
				return false, err
			}
			specReplicas := int32(1) // default
			if dep.Spec.Replicas != nil {
				specReplicas = *dep.Spec.Replicas
			}

			if specReplicas != dep.Status.Replicas ||
				specReplicas != dep.Status.ReadyReplicas ||
				specReplicas != dep.Status.AvailableReplicas {
				s.log.Info(
					"deployment is not ready yet",
					"name", name,
					"spec.replicas", specReplicas,
					"status.replicas", dep.Status.Replicas,
					"status.readyReplicas", dep.Status.ReadyReplicas,
					"status.availableReplicas", dep.Status.AvailableReplicas,
				)
				return false, nil
			}
			return true, nil
		},
	)
}

func (s *stateMachine) waitForAppPodsDeleted(name string) error {
	s.log.Info("waitForAppPodsDeleted", "name", name)

	return wait.PollUntilContextCancel(
		s.ctx,
		WaitForResourcesPollInterval,
		true,
		func(ctx context.Context) (bool, error) {
			podList := &corev1.PodList{}
			if err := s.cl.List(
				ctx,
				podList,
				client.InNamespace(consts.ModuleNamespace),
				client.MatchingLabelsSelector{
					Selector: labels.SelectorFromSet(
						labels.Set{"app": name},
					),
				},
			); err != nil {
				return false, fmt.Errorf("listing pods with app=%s: %w", name, err)
			}

			if len(podList.Items) > 0 {
				podNames := make([]string, 0, len(podList.Items))
				for _, p := range podList.Items {
					podNames = append(podNames, p.Name)
				}
				s.log.Info(
					"waiting for pods to be deleted: "+strings.Join(podNames, ", "),
					"n", len(podList.Items),
				)
				return false, nil
			}
			return true, nil
		},
	)
}

func (s *stateMachine) moveToDone() error {
	for conf := range allCertConfigs() {
		secret, err := s.getSecret(conf.TLSSecretName, false)
		if err != nil {
			return err
		}

		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		delete(secret.Labels, consts.SecretCertHookSuppressedByLabel)

		if err := s.cl.Update(s.ctx, secret); err != nil {
			return fmt.Errorf("deleting label from secret %s: %w", secret.Name, err)
		}
	}

	s.trigger.SetFinalizers(nil)
	utils.MapEnsureAndSet(&s.trigger.Labels, ConfigMapCompletedLabel, "true")
	delete(s.trigger.Labels, ConfigMapInProgressLabel)
	return nil
}
