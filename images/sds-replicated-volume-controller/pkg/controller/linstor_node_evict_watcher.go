package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	lclient "github.com/LINBIT/golinstor/client"
	"github.com/LINBIT/golinstor/devicelayerkind"
	v12 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	v13 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/strings/slices"
	"os/exec"
	"regexp"
	"sds-replicated-volume-controller/pkg/logger"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"time"
)

const (
	LinstorNodeEvictCtrlName = "linstor-node-evict-watcher"
	EvictNodeLabel           = "storage.deckhouse.io/sds-drbd-evict"

	LinstorCtrlDeploymentName             = "linstor-controller"
	SdsReplicatedVolumeCtrlDeploymentName = "sds-replicated-volume"
	SdsReplicatedVolumeNamespace          = "d8-sds-replicated-volume"

	targetVersion = "1.20.2"
)

var (
	faultyFlags = []string{"DELETE", "DRBD_DELETE", "INACTIVE"}
)

func RunLinstorNodeEvictWatcher(
	mgr manager.Manager,
	lc *lclient.Client,
	configSecretName string,
	interval int,
	log logger.Logger,
) (controller.Controller, error) {
	//cl := mgr.GetClient()

	c, err := controller.New(LinstorNodeEvictCtrlName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			log.Info(fmt.Sprintf("Starts the reconciliation for the request: %s", request.NamespacedName.Name))
			if request.Name == configSecretName {
				log.Debug(fmt.Sprintf("[RunLocalCSINodeWatcherController] request name %s matches the target config secret name %s. Start to reconcile", request.Name, configSecretName))

				return reconcile.Result{
					RequeueAfter: time.Duration(interval) * time.Second,
				}, nil
			}

			return reconcile.Result{}, nil
		}),
	})

	if err != nil {
		return nil, err
	}

	err = c.Watch(source.Kind(mgr.GetCache(), &v1.Secret{}), &handler.EnqueueRequestForObject{})

	return c, err
}

// фактически после евикта каждого ресурса каждой ноды мне нужно проводить все проверки, что все хорошо
func ReconcileLinstorNodeEviction(ctx context.Context, cl client.Client, lc *lclient.Client, log logger.Logger, secret *v1.Secret) error {
	log.Info(fmt.Sprintf("[ReconcileLinstorNodeEviction] starts the node eviction reconciliation"))

	log.Debug(fmt.Sprintf("[ReconcileLinstorNodeEviction] tries to get node selector from the config: %s", secret.Name))
	nodeSelector, err := GetNodeSelectorFromConfig(*secret)
	if err != nil {
		log.Error(err, fmt.Sprintf("[ReconcileLinstorNodeEviction] unable to get node selector from the secret %s", secret.Name))
		return err
	}
	log.Debug(fmt.Sprintf("[ReconcileLinstorNodeEviction] successfully got node selector from the config: %s", secret.Name))

	nodeSelector[EvictNodeLabel] = ""
	log.Debug(fmt.Sprintf("[ReconcileLinstorNodeEviction] tries to get nodes to be evicted by a selector: %v", nodeSelector))
	evictNodesList, err := GetKubernetesNodesBySelector(ctx, cl, nodeSelector)
	if err != nil {
		log.Error(err, fmt.Sprintf("[ReconcileLinstorNodeEviction] unable to nodes by selector %v", nodeSelector))
		return err
	}
	log.Debug(fmt.Sprintf("[ReconcileLinstorNodeEviction] successfully got nodes to be evicted by a selector: %v", nodeSelector))

	if len(evictNodesList.Items) == 0 {
		log.Info("[ReconcileLinstorNodeEviction] no nodes to be evicted found. Reconciliation stopped")
		return nil
	}
	for _, n := range evictNodesList.Items {
		log.Trace(fmt.Sprintf("[ReconcileLinstorNodeEviction] node %s is labeled as evicted", n.Name))
	}

	log.Info(fmt.Sprintf("[ReconcileLinstorNodeEviction] no Linstor faulty resources found. Start to check satellite connections"))
	faultySatellites, err := checkSatelliteConnections(ctx, lc, log)
	if err != nil {
		log.Error(err, "[ReconcileLinstorNodeEviction] unable to check satellite connections")
		return err
	}

	if len(faultySatellites) != 0 {
		err = errors.New("some satellites are not online")
		log.Error(err, fmt.Sprintf("[ReconcileLinstorNodeEviction] unable to run node eviction due to there are some satellites which are not online: %v", faultySatellites))
		return err
	}

	log.Debug("[ReconcileLinstorNodeEviction] tries to get Linstor resources for eviction")
	nodeResourcesToEvict, err := getEvictedNodesResources(ctx, lc, log, evictNodesList)
	if err != nil {
		log.Error(err, "[ReconcileLinstorNodeEviction] unable to get Linstor resources for evicted nodes")
		return err
	}
	log.Debug("[ReconcileLinstorNodeEviction] successfully got Linstor resources for eviction")

	// Здесь мы имеем список нод, которые реально нужно будет заевиктить
	log.Debug("[ReconcileLinstorNodeEviction] filter out nodes without resources")
	nodesToEvict := filterOutEvictNodesWithoutResources(log, evictNodesList, nodeResourcesToEvict)
	for _, n := range nodesToEvict {
		log.Trace(fmt.Sprintf("[ReconcileLinstorNodeEviction] node %s is going to be evicted", n.Name))
	}
	log.Debug("[ReconcileLinstorNodeEviction] successfully filtered out nodes without resources")

	log.Debug("[ReconcileLinstorNodeEviction] check if evicted nodes are cordoned")
	uncordonedNodes := checkCordonedNodes(nodesToEvict)
	if len(uncordonedNodes) != 0 {
		err = errors.New("evicted node is not cordoned")
		log.Error(err, fmt.Sprintf("[ReconcileLinstorNodeEviction] unable to run node eviction due to some nodes as evicted are not cordoned: %v", uncordonedNodes))
		return err
	}
	log.Debug("[ReconcileLinstorNodeEviction] every evicted node is cordoned")

	log.Debug("[ReconcileLinstorNodeEviction] tries to check Linstor faulty resources")
	faultyResNames, faultySPNames, err := checkFaultyEntities(ctx, lc, log)
	if err != nil {
		log.Error(err, "[ReconcileLinstorNodeEviction] unable to check Linstor faulty resources")
		return err
	}
	log.Debug("[ReconcileLinstorNodeEviction] successfully checked Linstor faulty resources")

	if len(faultyResNames) != 0 ||
		len(faultySPNames) != 0 {
		err = errors.New("faulty entities found")
		log.Error(err, fmt.Sprintf("[ReconcileLinstorNodeEviction] unable to run node eviction due to there are faulty resources: %v or faulty storage pools: %v", faultyResNames, faultySPNames))
		return err
	}
	log.Debug("[ReconcileLinstorNodeEviction] no Linstor faulty resources found")

	err = waitResourceSync(ctx, lc, log, 0)
	if err != nil {
		log.Error(err, "[ReconcileLinstorNodeEviction] unable to run node eviction due to an error has occurred while checking resource synchronization")
		return err
	}

	return nil
}

func createBackup(ctx context.Context, cl client.Client, lc *lclient.Client, log logger.Logger, dirPath string) error {
	log.Debug(fmt.Sprintf("[createBackup] tries to get the deployment %s", LinstorCtrlDeploymentName))
	linstorDep := &v12.Deployment{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      LinstorCtrlDeploymentName,
		Namespace: SdsReplicatedVolumeNamespace,
	}, linstorDep)
	if err != nil {
		return err
	}
	log.Debug(fmt.Sprintf("[createBackup] successfully got the deployment %s", LinstorCtrlDeploymentName))

	log.Debug(fmt.Sprintf("[createBackup] tries to get the deployment %s", SdsReplicatedVolumeCtrlDeploymentName))
	sdsReplicatedDep := &v12.Deployment{}
	err = cl.Get(ctx, client.ObjectKey{
		Name:      LinstorCtrlDeploymentName,
		Namespace: SdsReplicatedVolumeNamespace,
	}, sdsReplicatedDep)
	if err != nil {
		return err
	}
	log.Debug(fmt.Sprintf("[createBackup] successfully got the deployment %s", SdsReplicatedVolumeCtrlDeploymentName))

	linstorReplicasCount := *linstorDep.Spec.Replicas
	log.Trace(fmt.Sprintf("[createBackup] %s replicas cound: %d", LinstorCtrlDeploymentName, linstorReplicasCount))
	sdsReplicasCount := *sdsReplicatedDep.Spec.Replicas
	log.Trace(fmt.Sprintf("[createBackup] %s replicas cound: %d", SdsReplicatedVolumeCtrlDeploymentName, sdsReplicasCount))

	var zero int32

	log.Info(fmt.Sprintf("[createBackup] starts to scale down the deployment %s", LinstorCtrlDeploymentName))
	linstorDep.Spec.Replicas = &zero
	err = cl.Update(ctx, linstorDep)
	if err != nil {
		log.Error(err, fmt.Sprintf("[createBackup] unable to update the deployment %s", LinstorCtrlDeploymentName))
		return err
	}

	log.Info(fmt.Sprintf("[createBackup] starts to scale down the deployment %s", SdsReplicatedVolumeCtrlDeploymentName))
	sdsReplicatedDep.Spec.Replicas = &zero
	err = cl.Update(ctx, sdsReplicatedDep)
	if err != nil {
		log.Error(err, fmt.Sprintf("[createBackup] unable to update the deployment %s", SdsReplicatedVolumeCtrlDeploymentName))
		return err
	}

	log.Debug(fmt.Sprintf("[createBackup] waiting for the deployment %s pods to got downed", LinstorCtrlDeploymentName))
	err = waitForDeploymentToScaleDown(ctx, cl, log, LinstorCtrlDeploymentName)
	if err != nil {
		log.Error(err, fmt.Sprintf("[createBackup] unable to wait for deployment %s to scale down", LinstorCtrlDeploymentName))
		return err
	}

	log.Debug(fmt.Sprintf("[createBackup] waiting for the deployment %s pods to got downed", SdsReplicatedVolumeCtrlDeploymentName))
	err = waitForDeploymentToScaleDown(ctx, cl, log, SdsReplicatedVolumeCtrlDeploymentName)
	if err != nil {
		log.Error(err, fmt.Sprintf("[createBackup] unable to wait for deployment %s to scale down", SdsReplicatedVolumeCtrlDeploymentName))
		return err
	}

	log.Info("[createBackup] creates a backup of the Linstor database")
	if dirPath == "" {
		dirPath = "linstor_db_backup_before_evict"
	}
	dir := fmt.Sprintf("%s_%s", dirPath, time.Now().String())
	log.Debug(fmt.Sprintf("[createBackup] creates a dirPath %s to store a Linstor backup", dir))
	err = makeDir(dir)
	if err != nil {
		log.Error(err, fmt.Sprintf("[createBackup] unable to create a dirPath %s", dir))
		return err
	}

	crds := &v13.CustomResourceDefinitionList{}
	err = cl.List(ctx, crds)
	if err != nil {
		log.Error(err, "[createBackup] unable to list CustomResourceDefinitions")
		return err
	}

	linstorCrds, err := filterOutLinstorCRDs(crds)
	if err != nil {
		log.Error(err, "[createBackup] unable to filter out Linstor CRDs")
	}

	// TODO: write linstor crds to file
	//snapshotName, err := lc.Backup.Create(ctx)

	return nil
}

func filterOutLinstorCRDs(crds *v13.CustomResourceDefinitionList) ([]v13.CustomResourceDefinition, error) {
	linstorCRDs := make([]v13.CustomResourceDefinition, 0, len(crds.Items))

	for _, crd := range crds.Items {
		matched, err := regexp.MatchString(".*.internal.linstor.linbit.com", crd.Name)
		if err != nil {
			return nil, err
		}

		if matched {
			linstorCRDs = append(linstorCRDs, crd)
		}
	}

	return linstorCRDs, nil
}

func makeDir(dir string) error {
	cmd := exec.Command("mkdir", dir)
	stdErr := &bytes.Buffer{}
	cmd.Stderr = stdErr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("unable to run cmd: %s, err: %w, stderr: %s", cmd.String(), err, stdErr.String())
	}

	return nil
}

func waitForDeploymentToScaleDown(ctx context.Context, cl client.Client, log logger.Logger, deploymentName string) error {
	const maxAttempts = 60

	podsCount := 1
	attempts := 0
	linstorPods := &v1.PodList{}
	labelSelector := labels.Set(map[string]string{"app": deploymentName}).AsSelector()

	for podsCount > 0 || attempts < maxAttempts {
		err := cl.List(ctx, linstorPods, &client.ListOptions{Namespace: SdsReplicatedVolumeNamespace}, &client.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return err
		}

		podsCount = len(linstorPods.Items)

		if podsCount > 0 {
			log.Info(fmt.Sprintf("[waitForDeploymentToScaleDown] pods for the deployment %s in the namespace %s are still not down. Waiting...", deploymentName, SdsReplicatedVolumeNamespace))
			time.Sleep(5 * time.Second)
			attempts++
			continue
		}
	}

	if attempts == maxAttempts {
		err := errors.New(fmt.Sprintf("%s deployment's pods are still up", deploymentName))
		log.Error(err, "[waitForDeploymentToScaleDown] waiting for the pods to be downed has been interrupted")
		return err
	}

	return nil
}

func filterOutEvictNodesWithoutResources(log logger.Logger, nodes *v1.NodeList, nodesWithResources map[string][]lclient.ResourceWithVolumes) []v1.Node {
	filtered := make([]v1.Node, 0, len(nodes.Items))

	for _, n := range nodes.Items {
		if len(nodesWithResources[n.Name]) != 0 {
			log.Trace(fmt.Sprintf("[filterOutEvictNodesWithoutResources] evicted node %s has %d resources", n.Name, len(nodesWithResources[n.Name])))
			filtered = append(filtered, n)
		} else {
			log.Debug(fmt.Sprintf("[filterOutEvictNodesWithoutResources] evicted node %s has no resources. It will be skipped", n.Name))
		}
	}

	return filtered
}

func getEvictedNodesResources(ctx context.Context, lc *lclient.Client, log logger.Logger, nodes *v1.NodeList) (map[string][]lclient.ResourceWithVolumes, error) {
	rs, err := lc.Resources.GetResourceView(ctx)
	if err != nil {
		return nil, err
	}

	nodeResources := make(map[string][]lclient.ResourceWithVolumes, len(nodes.Items))
	for _, n := range nodes.Items {
		nodeResources[n.Name] = make([]lclient.ResourceWithVolumes, 0, len(rs))
	}

	for _, r := range rs {
		if _, exist := nodeResources[r.NodeName]; exist {
			log.Debug(fmt.Sprintf("[getEvictedNodesResources] a Linstor resource %s belongs to the evicted node %s", r.Name, r.NodeName))
			nodeResources[r.NodeName] = append(nodeResources[r.NodeName], r)
		} else {
			log.Debug(fmt.Sprintf("[getEvictedNodesResources] a Linstor resource %s does not belong to any evicted node", r.Name))
		}
	}

	return nodeResources, nil
}

func checkCordonedNodes(nodes []v1.Node) []string {
	faulty := make([]string, 0, len(nodes))

	for _, n := range nodes {
		if !n.Spec.Unschedulable {
			faulty = append(faulty, n.Name)
		}
	}

	return faulty
}

func waitResourceSync(ctx context.Context, lc *lclient.Client, log logger.Logger, allowedSyncCount int) error {
	const (
		//TODO: кол-во попыток перенести в конфиг и туда же время ожидания между попытками
		// лейбировать ноду
		maxAttempts = 180
	)

	attempts := 0

	for attempts < maxAttempts {
		log.Trace(fmt.Sprintf("[waitResourceSync] maximum attempts count %d", maxAttempts))
		log.Debug(fmt.Sprintf("[waitResourceSync] tries to get Linstor resources. Attempt %d", attempts))
		rs, err := lc.Resources.GetResourceView(ctx)
		if err != nil {
			log.Error(err, "[waitResourceSync] unable to get Linstor resources")
			return err
		}
		log.Debug("[waitResourceSync] successfully got Linstor resources")

		currentSyncCount := 0
		for _, r := range rs {
			log.Debug(fmt.Sprintf("[waitResourceSync] starts to check the sync target volumes for the resource %s on the node %s", r.Name, r.NodeName))
			for _, v := range r.Volumes {
				if v.State.DiskState == "SyncTarget" {
					log.Debug(fmt.Sprintf("[waitResourceSync] volume of the resource %s on the node %s has SyncTarget state", r.Name, r.NodeName))
					currentSyncCount++
				}
			}
		}

		log.Trace(fmt.Sprintf("[waitResourceSync] maximum sync volumes count %d", allowedSyncCount))
		log.Trace(fmt.Sprintf("[waitResourceSync] current sync volumes count %d", currentSyncCount))
		if currentSyncCount > allowedSyncCount {
			return errors.New("current sync resources number is more than maximum allowed one")
		}

		if currentSyncCount == 0 {
			log.Debug("[waitResourceSync] no sync resources found. The check is completed")
			return nil
		}

		if currentSyncCount == allowedSyncCount {
			log.Warning("[waitResourceSync] current sync resources number is equal maximum allowed one. Wait until the synchronization ends")
			attempts++
			time.Sleep(1 * time.Second)
		}
	}

	return errors.New("maximum number of attempts reached. Resources are still syncing")
}

func checkSatelliteConnections(ctx context.Context, lc *lclient.Client, log logger.Logger) (map[string]struct{}, error) {
	nodes, err := lc.Nodes.GetAll(ctx)
	if err != nil {
		log.Error(err, fmt.Sprintf("[checkSatelliteConnections] unable to get linstor nodes"))
		return nil, err
	}

	faultyNodes := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		if n.Type == "SATELLITE" && n.ConnectionStatus != "Online" {
			log.Warning(fmt.Sprintf("[checkSatelliteConnections] satellite node %s is not online", n.Name))
			faultyNodes[n.Name] = struct{}{}
		}
	}

	return faultyNodes, nil
}

func checkFaultyEntities(ctx context.Context, lc *lclient.Client, log logger.Logger) (faultyResources, faultyStoragePools map[string]struct{}, err error) {
	log.Debug(fmt.Sprintf("[checkFaultyEntities] tries to get all Linstor resources"))
	resources, err := lc.Resources.GetResourceView(ctx)
	if err != nil {
		log.Error(err, fmt.Sprintf("[checkFaultyEntities] unable to get Linstor resources"))
		return
	}
	log.Debug(fmt.Sprintf("[checkFaultyEntities] successfully got all Linstor resources"))

	faultyResources = make(map[string]struct{}, len(resources))
	for _, r := range resources {
		log.Debug(fmt.Sprintf("[checkFaultyEntities] starts to check a Linstor resource %s on node %s", r.Name, r.NodeName))

		faulty := false

		log.Trace(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s flags %v", r.Name, r.NodeName, r.Flags))
		for _, f := range faultyFlags {
			if slices.Contains(r.Flags, f) {
				log.Warning(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s is faultyResources due to it has faultyResources flag: %s", r.Name, r.NodeName, f))
				faulty = true
				break
			}
		}
		if faulty {
			faultyResources[r.Name] = struct{}{}
			continue
		}

		log.Debug(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s does not have any of faulty flags %v. Starts to check its volumes", r.Name, r.NodeName, faultyFlags))
		for _, vlm := range r.Volumes {
			if !hasTopLayerAsDRBD(vlm) {
				log.Debug(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s volume on node %s does not have top layer as DRBD, it will be skipped", r.Name, r.NodeName))
				continue
			}
			log.Debug(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s does have top layer as DRBD. Starts to check volume's state", r.Name, r.NodeName))

			log.Trace(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s has a volume state %s", r.Name, r.NodeName, vlm.State.DiskState))
			switch vlm.State.DiskState {
			case "Diskless":
				if !slices.Contains(r.Flags, "DISKLESS") {
					faulty = true
					log.Warning(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s is faulty due to its volume has DISKLESS state but the resource doesn't contains corresponding flag", r.Name, r.NodeName))
				}
			case "DUnknown", "Inconsistent", "Failed", "To: Creating", "To: Attachable", "To: Attaching":
				faulty = true
				log.Warning(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s is faulty due to its volume has a state %s", r.Name, r.NodeName, vlm.State.DiskState))
			case "UpToDate", "Created", "Attached":
				log.Debug(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s has a volume state %s", r.Name, r.NodeName, vlm.State.DiskState))
			default:
				log.Warning(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s is faulty due to its volume has an unexpected state %s", r.Name, r.NodeName, vlm.State.DiskState))
				faulty = true
			}
		}
		if faulty {
			faultyResources[r.Name] = struct{}{}
			continue
		}

		log.Debug(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s has successfully passed volumes check. Starts to check its properties", r.Name, r.NodeName))

		log.Debug(fmt.Sprintf("[checkFaultyEntities] tries to get a Linstor version"))
		version, err := lc.Controller.GetVersion(ctx)
		if err != nil {
			log.Error(err, fmt.Sprintf("[checkFaultyEntities] unable to get a Linstor version"))
			faultyResources[r.Name] = struct{}{}
			continue
		}
		log.Debug(fmt.Sprintf("[checkFaultyEntities] successfully got a Linstor version"))

		log.Trace(fmt.Sprintf("[checkFaultyEntities] a Linstor controller version %s", version.Version))
		log.Trace(fmt.Sprintf("[checkFaultyEntities] a Linstor target version %s", targetVersion))

		// TODO: add effective_properties check when the our Linstor api got version elder than 1.20.2

		for connection, v := range r.LayerObject.Drbd.Connections {
			if !v.Connected {
				log.Warning(fmt.Sprintf("[checkFaultyEntities] a Linstor resource %s on the node %s is faulty due to its connection %s is not in a connected state", r.Name, r.NodeName, connection))
				faulty = true
			}
		}

		if faulty {
			faultyResources[r.Name] = struct{}{}
			continue
		}
	}

	storagePools, err := lc.Nodes.GetStoragePoolView(ctx)
	if err != nil {
		log.Error(err, "[checkFaultyEntities] unable to get Linstor storage pools")
		return
	}

	faultyStoragePools = make(map[string]struct{}, len(storagePools))
	for _, sp := range storagePools {
		if len(sp.Reports) != 0 {
			faultyStoragePools[sp.StoragePoolName] = struct{}{}
		}
	}

	return faultyResources, faultyStoragePools, nil
}

func hasTopLayerAsDRBD(vlm lclient.Volume) bool {
	if len(vlm.LayerDataList) == 0 {
		return false
	}

	return vlm.LayerDataList[0].Type == devicelayerkind.Drbd
}
