package manualcertrenewal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/utils"
	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	TriggerKeyStep                     = "step"
	TriggerKeyBackupDaemonSetAffinity  = "backup-daemonset-affinity-"
	TriggerKeyBackupDeploymentReplicas = "backup-deployment-replicas-"

	DeploymentNameSchedulerExtender = "linstor-scheduler-extender"
	DeploymentNameWebhooks          = "webhooks"
	DeploymentNameSpaas             = "spaas"
	DeploymentNameController        = "linstor-controller"
	DeploymentNameCsiController     = "linstor-csi-controller"

	DaemonSetNameCsiNode = "linstor-csi-node"
	DaemonSetNameNode    = "linstor-node"

	WaitForResourcesPollInterval = time.Second
)

var (
	DaemonSetNameList = []string{DaemonSetNameCsiNode, DaemonSetNameNode}
	DaemonSetNameMap  = maps.Collect(
		utils.IterToKeys(slices.Values(DaemonSetNameList)),
	)

	DeploymentNameList = []string{
		DeploymentNameSchedulerExtender,
		DeploymentNameWebhooks,
		DeploymentNameSpaas,
		DeploymentNameController,
		DeploymentNameCsiController,
	}
	DeploymentNameMap = maps.Collect(
		utils.IterToKeys(slices.Values(DeploymentNameList)),
	)

	ModuleSelector = labels.SelectorFromSet(
		labels.Set{"module": consts.ModuleLabelValue},
	)
)

type step struct {
	Name    string
	Doc     string
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

	cachedDaemonSets  map[string]*appsv1.DaemonSet
	cachedDeployments map[string]*appsv1.Deployment

	daemonSets  *appsv1.DaemonSetList
	deployments *appsv1.DeploymentList
}

func newStateMachine(
	ctx context.Context,
	cl client.Client,
	log pkg.Logger,
	trigger *v1.ConfigMap,
) (*stateMachine, error) {
	s := &stateMachine{}
	// change sequence of steps here
	steps := []step{
		{
			Name:    "Initialized",
			Doc:     ``,
			Execute: s.initialize,
		},
		{
			Name: "TurnedOffCsiNode",
			Doc:  `TODO: terminating pods`,
			Execute: func() error {
				return s.turnOffDaemonSetAndWait(DaemonSetNameCsiNode)
			},
		},
		{
			Name: "TurnedOffCsiController",
			Doc:  ``,
			Execute: func() error {
				return s.turnOffDeploymentAndWait(DeploymentNameCsiController)
			},
		},
		{
			Name: "TurnedOffSchedulerExtender",
			Doc:  ``,
			Execute: func() error {
				return s.turnOffDeploymentAndWait(DeploymentNameSchedulerExtender)
			},
		},
		{
			Name: "TurnedOffSpaas",
			Doc:  ``,
			Execute: func() error {
				return s.turnOffDeploymentAndWait(DeploymentNameSpaas)
			},
		},
		{
			Name: "TurnedOffWebhooks",
			Doc:  ``,
			Execute: func() error {
				return s.turnOffDeploymentAndWait(DeploymentNameWebhooks)
			},
		},
		{
			Name: "TurnedOffNode",
			Doc:  ``,
			Execute: func() error {
				return s.turnOffDaemonSetAndWait(DaemonSetNameNode)
			},
		},
		{
			Name: "TurnedOffController",
			Doc:  ``,
			Execute: func() error {
				return s.turnOffDeploymentAndWait(DeploymentNameController)
			},
		},
		{
			Name:    "RenewedCerts",
			Doc:     ``,
			Execute: s.moveToRenewedCerts, // TODO
		},
		{
			Name: "TurnedOnController",
			Doc:  ``,
			Execute: func() error {
				return s.turnOnDeploymentAndWait(DeploymentNameController)
			},
		},
		{
			Name: "TurnedOnNode",
			Doc:  ``,
			Execute: func() error {
				return s.turnOnDaemonSetAndWait(DaemonSetNameNode)
			},
		},
		{
			Name: "TurnedOnWebhooks",
			Doc:  ``,
			Execute: func() error {
				return s.turnOnDeploymentAndWait(DeploymentNameWebhooks)
			},
		},
		{
			Name: "TurnedOnSpaas",
			Doc:  ``,
			Execute: func() error {
				return s.turnOnDeploymentAndWait(DeploymentNameSpaas)
			},
		},
		{
			Name: "TurnedOnSchedulerExtender",
			Doc:  ``,
			Execute: func() error {
				return s.turnOnDeploymentAndWait(DeploymentNameSchedulerExtender)
			},
		},
		{
			Name: "TurnedOnCsiController",
			Doc:  ``,
			Execute: func() error {
				return s.turnOnDeploymentAndWait(DeploymentNameCsiController)
			},
		},
		{
			Name: "TurnedOnCsiNode",
			Doc:  ``,
			Execute: func() error {
				return s.turnOnDaemonSetAndWait(DaemonSetNameCsiNode)
			},
		},
		{
			Name: "Done",
			Doc: `Renewal succeded.
Resource can optionally be deleted.
Remove label '%s' to restart.`,
			Execute: s.moveToDone,
		},
	}

	// determine current step
	stepName := trigger.Data[TriggerKeyStep]
	currentStepIdx := slices.IndexFunc(
		steps,
		func(s step) bool { return s.Name == stepName },
	)
	if currentStepIdx < 0 {
		return nil, fmt.Errorf("unknown step name: %s", stepName)
	}

	s.ctx = ctx
	s.cl = cl
	s.log = log
	s.trigger = trigger
	s.currentStepIdx = currentStepIdx
	s.steps = steps

	return s, nil
}

func (sm *stateMachine) run() error {
	for nextStep, ok := sm.nextStep(); ok; sm.currentStepIdx++ {
		if err := nextStep.Execute(); err != nil {
			return fmt.Errorf("executing step %s: %w", nextStep, err)
		}

		if err := sm.updateTrigger(nextStep); err != nil {
			return fmt.Errorf("updating trigger %s: %w", nextStep, err)
		}
	}
	return nil
}

func (s *stateMachine) nextStep() (step, bool) {
	if s.currentStepIdx < len(s.steps)-1 {
		return s.steps[s.currentStepIdx+1], true
	}
	return s.steps[s.currentStepIdx], false
}

func (s *stateMachine) updateTrigger(step step) error {
	utils.MapEnsureAndSet(&s.trigger.Data, TriggerKeyStep, step.Name)
	return s.cl.Update(s.ctx, s.trigger)
}

func (s *stateMachine) initialize() error {
	if err := s.checkResourcesHealth(); err != nil {
		return fmt.Errorf("checking resources: %w", err)
	}

	// checkPermissions
	if err := s.checkPermissions(); err != nil {
		return fmt.Errorf("checking permissions: %w", err)
	}

	// add finalizer
	s.trigger.SetFinalizers([]string{PackageUri})

	// add description
	// TODO
	utils.MapEnsureAndSet(
		&s.trigger.Data,
		"help",
		`
Manual Certificate Renewal.

This resource triggered certificate renewal process, which consist of the following steps:

`+
			strings.Join(
				slices.Collect(
					utils.IterMap(
						slices.Values(s.steps),
						func(s step) string {
							return fmt.Sprintf("\t- %s: %s", s.Name, s.Doc)
						},
					),
				),
				"\n",
			)+
			``,
	)

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

func (s *stateMachine) checkPermissions() error {
	var err error
	err = errors.Join(err, s.checkPermission("get", "DaemonSet", "apps"))
	err = errors.Join(err, s.checkPermission("list", "DaemonSet", "apps"))
	err = errors.Join(err, s.checkPermission("update", "DaemonSet", "apps"))
	err = errors.Join(err, s.checkPermission("watch", "DaemonSet", "apps"))
	err = errors.Join(err, s.checkPermission("get", "Deployment", "apps"))
	err = errors.Join(err, s.checkPermission("list", "Deployment", "apps"))
	err = errors.Join(err, s.checkPermission("update", "Deployment", "apps"))
	err = errors.Join(err, s.checkPermission("watch", "Deployment", "apps"))
	err = errors.Join(err, s.checkPermission("get", "Pod", ""))
	err = errors.Join(err, s.checkPermission("list", "Pod", ""))
	err = errors.Join(err, s.checkPermission("watch", "Pod", ""))
	return err
}

func (s *stateMachine) checkPermission(verb, resource, group string) error {
	sar := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: consts.ModuleNamespace,
				Verb:      verb,
				Group:     group,
				Resource:  resource,
			},
		},
	}

	if err := s.cl.Create(s.ctx, sar); err != nil {
		return fmt.Errorf("failed to check permission for %s on %s/%s: %w", verb, group, resource, err)
	}

	if !sar.Status.Allowed {
		return fmt.Errorf("permission denied: cannot %s %s/%s in namespace %s: %s",
			verb, group, resource, consts.ModuleNamespace, sar.Status.Reason)
	}

	s.log.Debug("permission granted", "verb", verb, "resource", resource, "group", group)
	return nil
}

func (s *stateMachine) turnOffDaemonSetAndWait(name string) error {
	ds, err := s.getDaemonSet(name, false)
	if err != nil {
		return err
	}

	// backup
	if affinityJson, err := json.Marshal(ds.Spec.Template.Spec.Affinity); err != nil {
		return fmt.Errorf("marshalling daemonset.template.spec.affinity for backup: %w", err)
	} else {
		utils.MapEnsureAndSet(
			&s.trigger.Data,
			TriggerKeyBackupDaemonSetAffinity+name,
			string(affinityJson),
		)
	}

	// turn off
	ds.Spec.Template.Spec.Affinity = &v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{}, // match no objects
				},
			},
		},
	}

	if err := s.cl.Update(s.ctx, ds); err != nil {
		return fmt.Errorf("updating daemonset %s: %w", name, err)
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
	depl, err := s.getDeployment(name, false)
	if err != nil {
		return err
	}

	// backup
	if replicasJson, err := json.Marshal(depl.Spec.Replicas); err != nil {
		return fmt.Errorf("marshalling deployment.spec.replicas for backup: %w", err)
	} else {
		utils.MapEnsureAndSet(
			&s.trigger.Data,
			TriggerKeyBackupDeploymentReplicas+name,
			string(replicasJson),
		)
	}

	// turn off
	depl.Spec.Replicas = new(int32)

	if err := s.cl.Update(s.ctx, depl); err != nil {
		return fmt.Errorf("updating deployment %s: %w", name, err)
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

func (s *stateMachine) turnOnDaemonSetAndWait(name string) error {
	ds, err := s.getDaemonSet(name, false)
	if err != nil {
		return err
	}

	originalAffinityJson := s.trigger.Data[TriggerKeyBackupDaemonSetAffinity]

	originalAffinity := &corev1.Affinity{}
	if err := json.Unmarshal([]byte(originalAffinityJson), originalAffinity); err != nil {
		return fmt.Errorf("unmarshalling original affinity to restore %s: %w", name, err)
	}

	ds.Spec.Template.Spec.Affinity = originalAffinity
	if err := s.cl.Update(s.ctx, ds); err != nil {
		return fmt.Errorf("updating daemonset %s: %w", name, err)
	}

	// wait
	if err := s.waitForDaemonSetReady(name); err != nil {
		return fmt.Errorf(
			"waiting for daemonset '%s' pods to be deleted: %w",
			name,
			err,
		)
	}
	// TODO
	return nil
}

func (s *stateMachine) turnOnDeploymentAndWait(name string) error {
	depl, err := s.getDeployment(name, false)
	if err != nil {
		return err
	}

	// restore original value
	replicasJson := s.trigger.Data[TriggerKeyBackupDeploymentReplicas]

	var originalReplicas = new(int32)
	if err := json.Unmarshal([]byte(replicasJson), originalReplicas); err != nil {
		return fmt.Errorf("unmarshalling original replicas to restore %s: %w", name, err)
	}

	depl.Spec.Replicas = originalReplicas
	if err := s.cl.Update(s.ctx, depl); err != nil {
		return fmt.Errorf("updating deployment %s: %w", name, err)
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
	return wait.PollUntilContextCancel(
		s.ctx,
		WaitForResourcesPollInterval,
		true,
		func(ctx context.Context) (bool, error) {
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
	return wait.PollUntilContextCancel(
		s.ctx,
		WaitForResourcesPollInterval,
		true,
		func(ctx context.Context) (bool, error) {
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
				s.log.Info("waiting for 'n' pods to be deleted", "n", len(podList.Items))
				return false, nil
			}
			return true, nil
		},
	)
}

func (s *stateMachine) moveToRenewedCerts() error {
	// TODO
	time.Sleep(5)
	return nil
}

func (s *stateMachine) moveToDone() error {
	s.trigger.SetFinalizers(nil)
	utils.MapEnsureAndSet(&s.trigger.Labels, ConfigMapCertRenewalCompletedLabel, "true")
	return nil
}
