package manualcertrenewal

// TODO
// step 0: init & backup
// step 1: everything off + renew certs
// step 2: everything on
// step 3: done

// TODO
// also update secrets before turning on

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/certificate"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/utils"
	"github.com/tidwall/gjson"
	appsv1 "k8s.io/api/apps/v1"
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

	DaemonSetNameCsiNode = "linstor-csi-node"
	DaemonSetNameNode    = "linstor-node"

	DeploymentNameSchedulerExtender = "linstor-scheduler-extender"
	DeploymentNameWebhooks          = "webhooks"
	DeploymentNameSpaas             = "spaas"
	DeploymentNameController        = "linstor-controller"
	DeploymentNameCsiController     = "linstor-csi-controller"

	WaitForResourcesPollInterval = time.Second
	CertExpirationThreshold      = time.Hour * 24 * 30 // 30d
)

var (
	DaemonSetNameList = []string{DaemonSetNameCsiNode, DaemonSetNameNode}

	DeploymentNameList = []string{
		DeploymentNameSchedulerExtender,
		DeploymentNameWebhooks,
		DeploymentNameSpaas,
		DeploymentNameController,
		DeploymentNameCsiController,
	}
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

	values pkg.PatchableValuesCollector
}

func newStateMachine(
	ctx context.Context,
	cl client.Client,
	log pkg.Logger,
	trigger *v1.ConfigMap,
) (*stateMachine, error) {
	s := &stateMachine{}

	// step 0: init & backup
	// step 1: everything off + renew certs
	// step 2: everything on
	// step 3: done

	steps := []step{
		{
			Name:    "Prepared",
			Doc:     ``,
			Execute: s.prepare,
		},
		{
			Name:    "TurnedOffAndRenewedCerts",
			Doc:     ``,
			Execute: s.turnOffAndRenewCerts,
		},
		{
			Name:    "TurnedOn",
			Doc:     ``,
			Execute: s.turnOn,
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

func (s *stateMachine) prepare() error {
	if err := s.checkResourcesHealth(); err != nil {
		return fmt.Errorf("checking resources: %w", err)
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

	return nil
}

func (s *stateMachine) backupDaemonSet(name string) error {
	// TODO
	ds, err := s.getDaemonSet(name, false)
	if err != nil {
		return err
	}

	if affinityJson, err := json.Marshal(ds.Spec.Template.Spec.Affinity); err != nil {
		return fmt.Errorf("marshalling daemonset.template.spec.affinity for backup: %w", err)
	} else {
		utils.MapEnsureAndSet(
			&s.trigger.Data,
			TriggerKeyBackupDaemonSetAffinity+name,
			string(affinityJson),
		)
	}

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

	if err := s.turnOffDaemonSetAndWait(DaemonSetNameNode); err != nil {
		return err
	}

	if err := s.turnOffDeploymentAndWait(DeploymentNameController); err != nil {
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

func (s *stateMachine) turnOn() error {
	if err := s.turnOnDeploymentAndWait(DeploymentNameController); err != nil {
		return fmt.Errorf("turnOnDeploymentAndWait: %w", err)
	}

	if err := s.turnOnDaemonSetAndWait(DaemonSetNameNode); err != nil {
		return fmt.Errorf("turnOnDaemonSetAndWait: %w", err)
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameWebhooks); err != nil {
		return fmt.Errorf("turnOnDeploymentAndWait: %w", err)
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameSpaas); err != nil {
		return fmt.Errorf("turnOnDeploymentAndWait: %w", err)
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameSchedulerExtender); err != nil {
		return fmt.Errorf("turnOnDeploymentAndWait: %w", err)
	}

	if err := s.turnOnDeploymentAndWait(DeploymentNameCsiController); err != nil {
		return fmt.Errorf("turnOnDeploymentAndWait: %w", err)
	}

	if err := s.turnOnDaemonSetAndWait(DaemonSetNameCsiNode); err != nil {
		return fmt.Errorf("turnOnDaemonSetAndWait: %w", err)
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

func (s *stateMachine) renewCerts() error {
	internal := s.values.Get(consts.ModuleName + ".internal")
	if !internal.Exists() {
		s.log.Warn(consts.ModuleName + ".internal not found in values, nothing to renew")
		return nil
	}

	// search for paths like: ".internal.{key}.[crt|ca]",
	internal.ForEach(func(key, value gjson.Result) bool {
		pathCrt := fmt.Sprintf("%s.internal.%s", consts.ModuleName, key.String())
		if err := s.renewCertIfNeeded(
			pathCrt,
			value,
			"crt",
		); err != nil {
			s.log.Error("error checking certificate", "err", err, "path", pathCrt, "ext", "crt")
		}

		pathCa := fmt.Sprintf("%s.internal.%s", consts.ModuleName, key.String())
		if err := s.renewCertIfNeeded(
			pathCa,
			value,
			"ca",
		); err != nil {
			s.log.Error("error checking certificate", "err", err, "path", pathCa, "ext", "ca")
		}

		return true
	})
	return nil
}

func (s *stateMachine) renewCertIfNeeded(path string, value gjson.Result, ext string) error {
	if certValue := value.Get(ext); !certValue.Exists() {
		return nil
	}

	s.log.Info("found cert value to update", "path", path, "ext", ext, "type", value.Type.String())

	if value.Type != gjson.String {
		s.log.Warn("not a string, skip", "path", path, "ext", ext, "type", value.Type.String())
	}

	if expiring, err := certificate.IsCertificateExpiringSoon(
		[]byte(value.Str),
		CertExpirationThreshold,
	); err != nil {
		return fmt.Errorf("checking cert %s.%s expiration: %w", path, ext, err)
	} else if !expiring {
		s.log.Info("cert is fresh", "path", path, "ext", ext)
		return nil
	}

	// TODO

	// caCert, tlsCert, err := certificate.ParseCertificatesFromPEM(nil, nil, nil)

	// certificate.GenerateSelfSignedCert()

	s.log.Info("TODO: cert is going to be renewed", "path", path, "ext", ext)
	// s.values.Set(fmt.Sprintf("%s.%s", path, ext), "TODO")
	return nil
}

func (s *stateMachine) moveToDone() error {
	s.trigger.SetFinalizers(nil)
	utils.MapEnsureAndSet(&s.trigger.Labels, ConfigMapCertRenewalCompletedLabel, "true")
	return nil
}
