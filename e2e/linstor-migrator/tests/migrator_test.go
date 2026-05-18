/*
Copyright 2026 Flant JSC

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

package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sncv1 "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srvv1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	helpers "github.com/deckhouse/sds-replicated-volume/e2e/linstor-migrator/tests/helpers"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
)

func buildMigratorScenario(scenarioName string, label string, mode helpers.ScenarioMode) bool {
	var _ = Describe(scenarioName, Ordered, Label(label), func() {
		// Shared variables for all tests in the suite
		var (
			ctx            context.Context
			k8sClient      client.Client
			clientset      kubernetes.Interface
			restConfig     *rest.Config
			testClusterRes *cluster.TestClusterResources
			runID          string

			// State tracking
			linstorBefore  *helpers.LinstorState
			fileChecksums  map[string]string // podName -> checksum
			oldRSPs        []helpers.RSPBaseline
			podNames       []string
			autoExpected   []string
			manualExpected []string
		)

		BeforeAll(func() {
			ctx = context.Background()

			// Generate a unique run ID for this test
			runIDBytes := make([]byte, 4)
			_, _ = rand.Read(runIDBytes)
			runID = hex.EncodeToString(runIDBytes)

			slog.Info("Starting Linstor Migrator E2E Test", "runID", runID)

			// Bind the test cluster from BeforeSuite
			testClusterRes = testClusterResources
			Expect(testClusterRes).NotTo(BeNil(), "Test cluster must be initialized in BeforeSuite")
			Expect(testClusterRes.Kubeconfig).NotTo(BeNil(), "Test cluster kubeconfig must be available")

			restConfig = testClusterRes.Kubeconfig

			// Initialize Kubernetes clients with custom schemes for sds-node-configurator and sds-replicated-volume
			var err error
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed(), "Failed to add corev1 scheme")
			Expect(appsv1.AddToScheme(scheme)).To(Succeed(), "Failed to add appsv1 scheme")
			Expect(sncv1.AddToScheme(scheme)).To(Succeed(), "Failed to add sds-node-configurator scheme")
			Expect(srvv1.AddToScheme(scheme)).To(Succeed(), "Failed to add sds-replicated-volume scheme")

			k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme})
			Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes client")

			clientset, err = kubernetes.NewForConfig(restConfig)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes clientset")

			storageClassName := os.Getenv("TEST_CLUSTER_STORAGE_CLASS")
			Expect(storageClassName).NotTo(BeEmpty(), "TEST_CLUSTER_STORAGE_CLASS must be set for VirtualDisk attachment")
			migratorImageTag := os.Getenv("TEST_MIGRATOR_MPO_IMAGE_TAG")
			Expect(migratorImageTag).NotTo(BeEmpty(), "TEST_MIGRATOR_MPO_IMAGE_TAG must be set for ModulePullOverride update")
			previousRunID := os.Getenv("TEST_PREVIOUS_RUNID")

			setupResult, err := helpers.PrepareOrRestoreMigratorScenario(
				ctx,
				k8sClient,
				clientset,
				restConfig,
				testClusterRes,
				helpers.PrepareOrRestoreMigratorScenarioConfig{
					SetupConfig: helpers.MigratorSetupConfig{
						RunID:            runID,
						StorageClassName: storageClassName,
						Namespace:        helpers.TestNamespace,
						Mode:             mode,
					},
					PreviousRunID:  previousRunID,
					RequireFocused: true,
				},
			)
			Expect(err).NotTo(HaveOccurred(), "Failed to prepare/restore migrator scenario")

			runID = setupResult.RunID
			fileChecksums = setupResult.FileChecksums
			linstorBefore = setupResult.LinstorBefore
			oldRSPs = append([]helpers.RSPBaseline(nil), setupResult.OldRSPs...)
			podNames = append([]string(nil), setupResult.PodNames...)
			autoExpected = append([]string(nil), setupResult.AutoResources...)
			manualExpected = append([]string(nil), setupResult.ManualResources...)

			if previousRunID == "" {
				// Temporary helper: remove this once linstor-migrator is merged into main branch of this repository.
				err = helpers.UpdateModulePullOverrideAndWait(ctx, k8sClient, migratorImageTag)
				Expect(err).NotTo(HaveOccurred(), "Failed to update ModulePullOverride and wait for module readiness")
			}
		})

		It("should migrate to new control plane", func() {
			// Enable new control plane
			err := helpers.EnableNewControlPlane(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred(), "Failed to enable new control plane")

			// Wait for new control-plane components to become ready and old ones to be removed.
			err = helpers.WaitForControlPlaneComponentsMigrated(ctx, k8sClient, helpers.MigrationWaitTimeout)
			Expect(err).NotTo(HaveOccurred(), "Control-plane components did not switch within timeout")

			// Wait for migration to complete
			err = helpers.WaitForMigrationComplete(ctx, k8sClient, helpers.MigrationWaitTimeout)
			Expect(err).NotTo(HaveOccurred(), "Migration did not complete within timeout")
		})

		Describe("post-migration verification", func() {
			Describe("RSP resource checks", func() {
				var rspSnapshot *helpers.RSPSnapshot

				BeforeAll(func() {
					var err error
					rspSnapshot, err = helpers.CollectRSPSnapshot(ctx, k8sClient)
					Expect(err).NotTo(HaveOccurred(), "failed to collect RSP snapshot")
				})

				It("should remove old RSP names", func() {
					errs := helpers.CheckRSPOldNamesRemovedFromSnapshot(rspSnapshot, oldRSPs)
					Expect(errs).To(BeEmpty(), "RSP old names removal check failed")
				})

				It("should create migrated RSPs with "+helpers.AutoReplicatedStoragePoolNamePrefix+" name prefix", func() {
					errs := helpers.CheckRSPMigratedNamesExistFromSnapshot(rspSnapshot, oldRSPs)
					Expect(errs).To(BeEmpty(), "RSP migrated naming check failed")
				})

				It("should preserve RSP type in migrated RSPs", func() {
					errs := helpers.CheckRSPTypesMatchOldBaselinesFromSnapshot(rspSnapshot, oldRSPs)
					Expect(errs).To(BeEmpty(), "RSP type check failed")
				})

				It("should preserve RSP lvmVolumeGroups in migrated RSPs", func() {
					errs := helpers.CheckRSPLVMGroupsMatchOldBaselinesFromSnapshot(rspSnapshot, oldRSPs)
					Expect(errs).To(BeEmpty(), "RSP lvmVolumeGroups check failed")
				})
			})

			Describe("RVA resource checks", func() {
				var rvaSnapshot *helpers.RVASnapshot

				BeforeAll(func() {
					var err error
					rvaSnapshot, err = helpers.CollectRVASnapshot(ctx, k8sClient, linstorBefore)
					Expect(err).NotTo(HaveOccurred(), "failed to collect RVA snapshot")
				})

				It("should match RVA count to InUse linstor volumes", func() {
					errs := helpers.CheckRVACountMatchesInUseVolumesFromSnapshot(rvaSnapshot)
					Expect(errs).To(BeEmpty(), "RVA count check failed")
				})

				It("should have RVA for each InUse linstor volume pair", func() {
					errs := helpers.CheckRVAPairsMatchInUseVolumesFromSnapshot(rvaSnapshot)
					Expect(errs).To(BeEmpty(), "RVA pair mapping check failed")
				})

				It("should use expected RVA naming pattern", func() {
					errs := helpers.CheckRVANamePatternFromSnapshot(rvaSnapshot)
					Expect(errs).To(BeEmpty(), "RVA naming pattern check failed")
				})
			})

			Describe("RV resource checks", func() {
				var rvSnapshot *helpers.RVSnapshot

				BeforeAll(func() {
					var err error
					rvSnapshot, err = helpers.CollectRVSnapshot(ctx, k8sClient, linstorBefore)
					Expect(err).NotTo(HaveOccurred(), "failed to collect RV snapshot")
				})

				It("should match RV count to unique linstor resources", func() {
					errs := helpers.CheckRVCountMatchesLinstorResourcesFromSnapshot(rvSnapshot)
					Expect(errs).To(BeEmpty(), "RV count check failed")
				})

				It("should have RV names matching linstor resource names", func() {
					errs := helpers.CheckRVNamesMatchLinstorResourcesFromSnapshot(rvSnapshot)
					Expect(errs).To(BeEmpty(), "RV name mapping check failed")
				})

				It("should set maxAttachments to 2 for each RV", func() {
					errs := helpers.CheckRVMaxAttachmentsFromSnapshot(rvSnapshot)
					Expect(errs).To(BeEmpty(), "RV maxAttachments check failed")
				})

				It("should set proper configuration mode for each RV", func() {
					errs := helpers.CheckRVConfigurationModeFromSnapshot(rvSnapshot, autoExpected, manualExpected)
					Expect(errs).To(BeEmpty(), "RV configuration mode check failed")
				})

				It("should set RV replicatedStorageClassName from PV storageClassName", func() {
					errs := helpers.CheckRVReplicatedStorageClassMatchesPVFromSnapshot(rvSnapshot, autoExpected, manualExpected)
					Expect(errs).To(BeEmpty(), "RV replicatedStorageClassName check failed")
				})

				It("should set proper manualConfiguration for each RV", func() {
					errs := helpers.CheckRVManualConfigurationFromSnapshot(rvSnapshot, autoExpected, manualExpected)
					Expect(errs).To(BeEmpty(), "RV manualConfiguration check failed")
				})

				It("should set correct manualConfiguration fields for Manual RVs", func() {
					if len(manualExpected) == 0 {
						Skip("No manual resources to check")
					}
					errs := helpers.CheckRVManualConfigurationFieldsFromSnapshot(rvSnapshot, manualExpected)
					Expect(errs).To(BeEmpty(), "RV manualConfiguration fields check failed")
				})

				It("should set RV size equal to PV capacity", func() {
					errs := helpers.CheckRVSizeMatchesPVFromSnapshot(rvSnapshot)
					Expect(errs).To(BeEmpty(), "RV size check failed")
				})

				It("should remove adopt annotations from each RV", func() {
					errs := helpers.CheckRVAdoptAnnotationsAbsentFromSnapshot(rvSnapshot)
					Expect(errs).To(BeEmpty(), "RV adopt annotations check failed")
				})

				It("should not set no-persistent-volume label on each RV", func() {
					errs := helpers.CheckRVNoPersistentVolumeLabelAbsentFromSnapshot(rvSnapshot)
					Expect(errs).To(BeEmpty(), "RV no-persistent-volume label check failed")
				})
			})

			Describe("RVR resource checks", func() {
				var rvrSnapshot *helpers.RVRSnapshot

				BeforeAll(func() {
					var err error
					rvrSnapshot, err = helpers.CollectRVRSnapshot(ctx, k8sClient, linstorBefore, oldRSPs)
					Expect(err).NotTo(HaveOccurred(), "failed to collect RVR snapshot")
				})

				It("should match RVR count to linstor resources count", func() {
					errs := helpers.CheckRVRCountMatchesLinstorResourcesFromSnapshot(rvrSnapshot)
					Expect(errs).To(BeEmpty(), "RVR count check failed")
				})

				It("should match RVR names to migrator naming rule", func() {
					errs := helpers.CheckRVRNamesMatchLinstorFromSnapshot(rvrSnapshot)
					Expect(errs).To(BeEmpty(), "RVR name check failed")
				})

				It("should validate RVR spec identity fields", func() {
					errs := helpers.CheckRVRSpecIdentityFieldsFromSnapshot(rvrSnapshot)
					Expect(errs).To(BeEmpty(), "RVR spec identity fields check failed")
				})

				It("should validate RVR spec type mapping against linstor", func() {
					errs := helpers.CheckRVRSpecTypeMatchesLinstorFromSnapshot(rvrSnapshot)
					Expect(errs).To(BeEmpty(), "RVR spec type mapping check failed")
				})

				It("should validate Diskful RVR storage fields", func() {
					errs := helpers.CheckRVRSpecDiskfulStorageFieldsFromSnapshot(rvrSnapshot)
					Expect(errs).To(BeEmpty(), "RVR Diskful storage fields check failed")
				})

				It("should validate non-Diskful RVR storage fields are empty", func() {
					errs := helpers.CheckRVRSpecNonDiskfulStorageFieldsEmptyFromSnapshot(rvrSnapshot)
					Expect(errs).To(BeEmpty(), "RVR non-Diskful storage fields check failed")
				})
			})

			Describe("DRBDR resource checks", func() {
				var drbdrSnapshot *helpers.DRBDRSnapshot

				BeforeAll(func() {
					var err error
					drbdrSnapshot, err = helpers.CollectDRBDRSnapshot(ctx, k8sClient, linstorBefore)
					Expect(err).NotTo(HaveOccurred(), "failed to collect DRBDR snapshot")
				})

				It("should match DRBDR count to linstor resources count", func() {
					errs := helpers.CheckDRBDRCountMatchesLinstorResourcesFromSnapshot(drbdrSnapshot)
					Expect(errs).To(BeEmpty(), "DRBDR count check failed")
				})

				It("should match DRBDR names to linstor resource-nodeID pairs", func() {
					errs := helpers.CheckDRBDRNamesMatchLinstorFromSnapshot(drbdrSnapshot)
					Expect(errs).To(BeEmpty(), "DRBDR name check failed")
				})

				It("should validate DRBDR spec identity/default fields", func() {
					errs := helpers.CheckDRBDRSpecIdentityDefaultsFromSnapshot(drbdrSnapshot)
					Expect(errs).To(BeEmpty(), "DRBDR spec identity/default fields check failed")
				})

				It("should validate DRBDR spec type mapping against linstor", func() {
					errs := helpers.CheckDRBDRSpecTypeMatchesLinstorFromSnapshot(drbdrSnapshot)
					Expect(errs).To(BeEmpty(), "DRBDR spec type mapping check failed")
				})

				It("should validate DRBDR spec storage fields", func() {
					errs := helpers.CheckDRBDRSpecStorageFieldsFromSnapshot(drbdrSnapshot)
					Expect(errs).To(BeEmpty(), "DRBDR spec storage fields check failed")
				})

				It("should have DRBDR ownerReference to ReplicatedVolumeReplica with matching name", func() {
					errs := helpers.CheckDRBDROwnerReferenceFromSnapshot(drbdrSnapshot)
					Expect(errs).To(BeEmpty(), "DRBDR ownerReference check failed")
				})
			})

			Describe("LLV resource checks", func() {
				var llvSnapshot *helpers.LLVSnapshot

				BeforeAll(func() {
					var err error
					llvSnapshot, err = helpers.CollectLLVSnapshot(ctx, k8sClient, linstorBefore, oldRSPs)
					Expect(err).NotTo(HaveOccurred(), "failed to collect LLV snapshot")
				})

				It("should match LLV count to linstor volumes with backing_disk", func() {
					errs := helpers.CheckLLVCountMatchesLinstorBackingDiskVolumesFromSnapshot(llvSnapshot)
					Expect(errs).To(BeEmpty(), "LLV count check failed")
				})

				It("should match LLV names to RVR-prefix pattern", func() {
					errs := helpers.CheckLLVNamesMatchRVRPrefixFromSnapshot(llvSnapshot)
					Expect(errs).To(BeEmpty(), "LLV name check failed")
				})

				It("should validate LLV spec identity and placement fields", func() {
					errs := helpers.CheckLLVSpecIdentityAndPlacementFieldsFromSnapshot(llvSnapshot)
					Expect(errs).To(BeEmpty(), "LLV spec identity and placement fields check failed")
				})

				It("should validate LLV spec type and thin fields", func() {
					errs := helpers.CheckLLVSpecTypeAndThinFieldsFromSnapshot(llvSnapshot)
					Expect(errs).To(BeEmpty(), "LLV spec type and thin fields check failed")
				})

				It("should validate LLV spec size field", func() {
					errs := helpers.CheckLLVSpecSizeFieldFromSnapshot(llvSnapshot)
					Expect(errs).To(BeEmpty(), "LLV spec size field check failed")
				})

				It("should have LLV ownerReference to ReplicatedVolumeReplica with name matching LLV prefix", func() {
					errs := helpers.CheckLLVOwnerReferenceFromSnapshot(llvSnapshot)
					Expect(errs).To(BeEmpty(), "LLV ownerReference check failed")
				})
			})

			It("should verify migrator artifacts on master node", func() {
				slog.Debug("CHECK: Checking migrator artifacts on master node")
				errs := helpers.VerifyMigratorArtifactsOnMaster(ctx, k8sClient, clientset, restConfig)
				Expect(errs).To(BeEmpty(), "migrator artifacts check failed")
			})

			It("should verify no replicatedstoragemetadatabackups remain in the cluster", func() {
				err := helpers.VerifyNoReplicatedStorageMetadataBackupsExist(ctx, k8sClient)
				Expect(err).NotTo(HaveOccurred(), "replicatedstoragemetadatabackups cleanup check failed")
			})

			It("should verify no linstor CRDs remain in the cluster", func() {
				err := helpers.VerifyNoLinstorCRDExists(ctx, k8sClient)
				Expect(err).NotTo(HaveOccurred(), "linstor CRD cleanup check failed")
			})

			It("should verify file checksums", func() {
				slog.Debug("CHECK: Verifying file checksums (data integrity)")
				errs := helpers.VerifyAllChecksums(ctx, clientset, restConfig, podNames, fileChecksums)
				Expect(errs).To(BeEmpty(), "checksum verification failed")
			})

			It("should verify new control plane is running", func() {
				slog.Debug("CHECK: Checking new control plane is running")
				Expect(helpers.VerifyNewControlPlaneRunning(ctx, k8sClient)).NotTo(
					HaveOccurred(),
					"new control plane verification failed",
				)
			})

			It("should verify csi-node daemonset is running", func() {
				Expect(helpers.VerifyCSINodeDaemonSetRunning(ctx, k8sClient)).NotTo(
					HaveOccurred(),
					"csi-node daemonset verification failed",
				)
			})

			It("should verify old control plane is stopped", func() {
				Expect(helpers.VerifyOldControlPlaneStopped(ctx, k8sClient)).NotTo(
					HaveOccurred(),
					"old control plane verification failed",
				)
			})
		})
	})
	return true
}

var _ = buildMigratorScenario("All RVs are created with ConfigurationMode: Auto", "Auto", helpers.ScenarioModeAutoOnly)
var _ = buildMigratorScenario("All RVs are created with ConfigurationMode: Manual", "Manual", helpers.ScenarioModeManualOnly)
var _ = buildMigratorScenario("RVs are created with mixed ConfigurationMode (Auto and Manual)", "Mixed", helpers.ScenarioModeMixed)

var _ = Describe("Linstor resources without PV", Ordered, Label("WithoutPV"), func() {
	var (
		ctx            context.Context
		k8sClient      client.Client
		clientset      kubernetes.Interface
		restConfig     *rest.Config
		testClusterRes *cluster.TestClusterResources
		runID          string

		// State tracking
		linstorBefore  *helpers.LinstorState
		oldRSPs        []helpers.RSPBaseline
		podNames       []string
		autoExpected   []string
		manualExpected []string
	)

	BeforeAll(func() {
		ctx = context.Background()

		runIDBytes := make([]byte, 4)
		_, _ = rand.Read(runIDBytes)
		runID = hex.EncodeToString(runIDBytes)

		slog.Info("Starting Linstor Migrator E2E Test (WithoutPV)", "runID", runID)

		testClusterRes = testClusterResources
		Expect(testClusterRes).NotTo(BeNil(), "Test cluster must be initialized in BeforeSuite")
		Expect(testClusterRes.Kubeconfig).NotTo(BeNil(), "Test cluster kubeconfig must be available")

		restConfig = testClusterRes.Kubeconfig

		var err error
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed(), "Failed to add corev1 scheme")
		Expect(appsv1.AddToScheme(scheme)).To(Succeed(), "Failed to add appsv1 scheme")
		Expect(sncv1.AddToScheme(scheme)).To(Succeed(), "Failed to add sds-node-configurator scheme")
		Expect(srvv1.AddToScheme(scheme)).To(Succeed(), "Failed to add sds-replicated-volume scheme")

		k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes client")

		clientset, err = kubernetes.NewForConfig(restConfig)
		Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes clientset")

		storageClassName := os.Getenv("TEST_CLUSTER_STORAGE_CLASS")
		Expect(storageClassName).NotTo(BeEmpty(), "TEST_CLUSTER_STORAGE_CLASS must be set for VirtualDisk attachment")
		migratorImageTag := os.Getenv("TEST_MIGRATOR_MPO_IMAGE_TAG")
		Expect(migratorImageTag).NotTo(BeEmpty(), "TEST_MIGRATOR_MPO_IMAGE_TAG must be set for ModulePullOverride update")
		previousRunID := os.Getenv("TEST_PREVIOUS_RUNID")

		setupResult, err := helpers.PrepareOrRestoreMigratorScenario(
			ctx,
			k8sClient,
			clientset,
			restConfig,
			testClusterRes,
			helpers.PrepareOrRestoreMigratorScenarioConfig{
				SetupConfig: helpers.MigratorSetupConfig{
					RunID:            runID,
					StorageClassName: storageClassName,
					Namespace:        helpers.TestNamespace,
					Mode:             helpers.ScenarioModeManualOnly,
				},
				PreviousRunID:  previousRunID,
				RequireFocused: true,
			},
		)
		Expect(err).NotTo(HaveOccurred(), "Failed to prepare/restore migrator scenario")

		runID = setupResult.RunID
		linstorBefore = setupResult.LinstorBefore
		oldRSPs = append([]helpers.RSPBaseline(nil), setupResult.OldRSPs...)
		podNames = append([]string(nil), setupResult.PodNames...)
		autoExpected = append([]string(nil), setupResult.AutoResources...)
		manualExpected = append([]string(nil), setupResult.ManualResources...)

		if previousRunID == "" {
			err = helpers.UpdateModulePullOverrideAndWait(ctx, k8sClient, migratorImageTag)
			Expect(err).NotTo(HaveOccurred(), "Failed to update ModulePullOverride and wait for module readiness")

			// Simulate lost PVs — delete pods, PVCs, and PVs.
			err = helpers.SimulateLostPVs(ctx, k8sClient, podNames)
			Expect(err).NotTo(HaveOccurred(), "Failed to simulate lost PVs")
		}
	})

	It("should migrate to new control plane", func() {
		err := helpers.EnableNewControlPlane(ctx, k8sClient)
		Expect(err).NotTo(HaveOccurred(), "Failed to enable new control plane")

		err = helpers.WaitForControlPlaneComponentsMigrated(ctx, k8sClient, helpers.MigrationWaitTimeout)
		Expect(err).NotTo(HaveOccurred(), "Control-plane components did not switch within timeout")

		err = helpers.WaitForMigrationComplete(ctx, k8sClient, helpers.MigrationWaitTimeout)
		Expect(err).NotTo(HaveOccurred(), "Migration did not complete within timeout")
	})

	Describe("post-migration verification", func() {
		Describe("RSP resource checks", func() {
			var rspSnapshot *helpers.RSPSnapshot

			BeforeAll(func() {
				var err error
				rspSnapshot, err = helpers.CollectRSPSnapshot(ctx, k8sClient)
				Expect(err).NotTo(HaveOccurred(), "failed to collect RSP snapshot")
			})

			It("should remove old RSP names", func() {
				errs := helpers.CheckRSPOldNamesRemovedFromSnapshot(rspSnapshot, oldRSPs)
				Expect(errs).To(BeEmpty(), "RSP old names removal check failed")
			})

			It("should create migrated RSPs with "+helpers.AutoReplicatedStoragePoolNamePrefix+" name prefix", func() {
				errs := helpers.CheckRSPMigratedNamesExistFromSnapshot(rspSnapshot, oldRSPs)
				Expect(errs).To(BeEmpty(), "RSP migrated naming check failed")
			})

			It("should preserve RSP type in migrated RSPs", func() {
				errs := helpers.CheckRSPTypesMatchOldBaselinesFromSnapshot(rspSnapshot, oldRSPs)
				Expect(errs).To(BeEmpty(), "RSP type check failed")
			})

			It("should preserve RSP lvmVolumeGroups in migrated RSPs", func() {
				errs := helpers.CheckRSPLVMGroupsMatchOldBaselinesFromSnapshot(rspSnapshot, oldRSPs)
				Expect(errs).To(BeEmpty(), "RSP lvmVolumeGroups check failed")
			})
		})

		Describe("RV resource checks", func() {
			var rvSnapshot *helpers.RVSnapshot

			BeforeAll(func() {
				var err error
				rvSnapshot, err = helpers.CollectRVSnapshot(ctx, k8sClient, linstorBefore)
				Expect(err).NotTo(HaveOccurred(), "failed to collect RV snapshot")
			})

			It("should match RV count to unique linstor resources", func() {
				errs := helpers.CheckRVCountMatchesLinstorResourcesFromSnapshot(rvSnapshot)
				Expect(errs).To(BeEmpty(), "RV count check failed")
			})

			It("should have RV names matching linstor resource names", func() {
				errs := helpers.CheckRVNamesMatchLinstorResourcesFromSnapshot(rvSnapshot)
				Expect(errs).To(BeEmpty(), "RV name mapping check failed")
			})

			It("should set maxAttachments to 2 for each RV", func() {
				errs := helpers.CheckRVMaxAttachmentsFromSnapshot(rvSnapshot)
				Expect(errs).To(BeEmpty(), "RV maxAttachments check failed")
			})

			It("should set proper configuration mode for each RV", func() {
				errs := helpers.CheckRVConfigurationModeFromSnapshot(rvSnapshot, autoExpected, manualExpected)
				Expect(errs).To(BeEmpty(), "RV configuration mode check failed")
			})

			It("should set RV replicatedStorageClassName from pool name (without PV)", func() {
				errs := helpers.CheckRVReplicatedStorageClassWithoutPVSnapshot(rvSnapshot, autoExpected, manualExpected)
				Expect(errs).To(BeEmpty(), "RV replicatedStorageClassName check failed")
			})

			It("should set proper manualConfiguration for each RV", func() {
				errs := helpers.CheckRVManualConfigurationFromSnapshot(rvSnapshot, autoExpected, manualExpected)
				Expect(errs).To(BeEmpty(), "RV manualConfiguration check failed")
			})

			It("should set correct manualConfiguration fields for Manual RVs", func() {
				if len(manualExpected) == 0 {
					Skip("No manual resources to check")
				}
				errs := helpers.CheckRVManualConfigurationFieldsFromSnapshot(rvSnapshot, manualExpected)
				Expect(errs).To(BeEmpty(), "RV manualConfiguration fields check failed")
			})

			It("should remove adopt annotations from each RV", func() {
				errs := helpers.CheckRVAdoptAnnotationsAbsentFromSnapshot(rvSnapshot)
				Expect(errs).To(BeEmpty(), "RV adopt annotations check failed")
			})

			It("should set no-persistent-volume label on each RV", func() {
				errs := helpers.CheckRVNoPersistentVolumeLabelFromSnapshot(rvSnapshot)
				Expect(errs).To(BeEmpty(), "RV no-persistent-volume label check failed")
			})
		})

		Describe("RVR resource checks", func() {
			var rvrSnapshot *helpers.RVRSnapshot

			BeforeAll(func() {
				var err error
				rvrSnapshot, err = helpers.CollectRVRSnapshot(ctx, k8sClient, linstorBefore, oldRSPs)
				Expect(err).NotTo(HaveOccurred(), "failed to collect RVR snapshot")
			})

			It("should match RVR count to linstor resources count", func() {
				errs := helpers.CheckRVRCountMatchesLinstorResourcesFromSnapshot(rvrSnapshot)
				Expect(errs).To(BeEmpty(), "RVR count check failed")
			})

			It("should match RVR names to migrator naming rule", func() {
				errs := helpers.CheckRVRNamesMatchLinstorFromSnapshot(rvrSnapshot)
				Expect(errs).To(BeEmpty(), "RVR name check failed")
			})

			It("should validate RVR spec identity fields", func() {
				errs := helpers.CheckRVRSpecIdentityFieldsFromSnapshot(rvrSnapshot)
				Expect(errs).To(BeEmpty(), "RVR spec identity fields check failed")
			})

			It("should validate RVR spec type mapping against linstor", func() {
				errs := helpers.CheckRVRSpecTypeMatchesLinstorFromSnapshot(rvrSnapshot)
				Expect(errs).To(BeEmpty(), "RVR spec type mapping check failed")
			})

			It("should validate Diskful RVR storage fields", func() {
				errs := helpers.CheckRVRSpecDiskfulStorageFieldsFromSnapshot(rvrSnapshot)
				Expect(errs).To(BeEmpty(), "RVR Diskful storage fields check failed")
			})

			It("should validate non-Diskful RVR storage fields are empty", func() {
				errs := helpers.CheckRVRSpecNonDiskfulStorageFieldsEmptyFromSnapshot(rvrSnapshot)
				Expect(errs).To(BeEmpty(), "RVR non-Diskful storage fields check failed")
			})
		})

		Describe("DRBDR resource checks", func() {
			var drbdrSnapshot *helpers.DRBDRSnapshot

			BeforeAll(func() {
				var err error
				drbdrSnapshot, err = helpers.CollectDRBDRSnapshot(ctx, k8sClient, linstorBefore)
				Expect(err).NotTo(HaveOccurred(), "failed to collect DRBDR snapshot")
			})

			It("should match DRBDR count to linstor resources count", func() {
				errs := helpers.CheckDRBDRCountMatchesLinstorResourcesFromSnapshot(drbdrSnapshot)
				Expect(errs).To(BeEmpty(), "DRBDR count check failed")
			})

			It("should match DRBDR names to linstor resource-nodeID pairs", func() {
				errs := helpers.CheckDRBDRNamesMatchLinstorFromSnapshot(drbdrSnapshot)
				Expect(errs).To(BeEmpty(), "DRBDR name check failed")
			})

			It("should validate DRBDR spec identity/default fields", func() {
				errs := helpers.CheckDRBDRSpecIdentityDefaultsFromSnapshot(drbdrSnapshot)
				Expect(errs).To(BeEmpty(), "DRBDR spec identity/default fields check failed")
			})

			It("should validate DRBDR spec type mapping against linstor", func() {
				errs := helpers.CheckDRBDRSpecTypeMatchesLinstorFromSnapshot(drbdrSnapshot)
				Expect(errs).To(BeEmpty(), "DRBDR spec type mapping check failed")
			})

			It("should validate DRBDR spec storage fields", func() {
				errs := helpers.CheckDRBDRSpecStorageFieldsFromSnapshot(drbdrSnapshot)
				Expect(errs).To(BeEmpty(), "DRBDR spec storage fields check failed")
			})

			It("should have DRBDR ownerReference to ReplicatedVolumeReplica with matching name", func() {
				errs := helpers.CheckDRBDROwnerReferenceFromSnapshot(drbdrSnapshot)
				Expect(errs).To(BeEmpty(), "DRBDR ownerReference check failed")
			})
		})

		Describe("LLV resource checks", func() {
			var llvSnapshot *helpers.LLVSnapshot

			BeforeAll(func() {
				var err error
				llvSnapshot, err = helpers.CollectLLVSnapshot(ctx, k8sClient, linstorBefore, oldRSPs)
				Expect(err).NotTo(HaveOccurred(), "failed to collect LLV snapshot")
			})

			It("should match LLV count to linstor volumes with backing_disk", func() {
				errs := helpers.CheckLLVCountMatchesLinstorBackingDiskVolumesFromSnapshot(llvSnapshot)
				Expect(errs).To(BeEmpty(), "LLV count check failed")
			})

			It("should match LLV names to RVR-prefix pattern", func() {
				errs := helpers.CheckLLVNamesMatchRVRPrefixFromSnapshot(llvSnapshot)
				Expect(errs).To(BeEmpty(), "LLV name check failed")
			})

			It("should validate LLV spec identity and placement fields", func() {
				errs := helpers.CheckLLVSpecIdentityAndPlacementFieldsFromSnapshot(llvSnapshot)
				Expect(errs).To(BeEmpty(), "LLV spec identity and placement fields check failed")
			})

			It("should validate LLV spec type and thin fields", func() {
				errs := helpers.CheckLLVSpecTypeAndThinFieldsFromSnapshot(llvSnapshot)
				Expect(errs).To(BeEmpty(), "LLV spec type and thin fields check failed")
			})

			It("should validate LLV spec size field", func() {
				errs := helpers.CheckLLVSpecSizeFieldFromSnapshot(llvSnapshot)
				Expect(errs).To(BeEmpty(), "LLV spec size field check failed")
			})

			It("should have LLV ownerReference to ReplicatedVolumeReplica with name matching LLV prefix", func() {
				errs := helpers.CheckLLVOwnerReferenceFromSnapshot(llvSnapshot)
				Expect(errs).To(BeEmpty(), "LLV ownerReference check failed")
			})
		})

		It("should verify migrator artifacts on master node", func() {
			slog.Debug("CHECK: Checking migrator artifacts on master node")
			errs := helpers.VerifyMigratorArtifactsOnMaster(ctx, k8sClient, clientset, restConfig)
			Expect(errs).To(BeEmpty(), "migrator artifacts check failed")
		})

		It("should verify no replicatedstoragemetadatabackups remain in the cluster", func() {
			err := helpers.VerifyNoReplicatedStorageMetadataBackupsExist(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred(), "replicatedstoragemetadatabackups cleanup check failed")
		})

		It("should verify no linstor CRDs remain in the cluster", func() {
			err := helpers.VerifyNoLinstorCRDExists(ctx, k8sClient)
			Expect(err).NotTo(HaveOccurred(), "linstor CRD cleanup check failed")
		})

		It("should verify new control plane is running", func() {
			slog.Debug("CHECK: Checking new control plane is running")
			Expect(helpers.VerifyNewControlPlaneRunning(ctx, k8sClient)).NotTo(
				HaveOccurred(),
				"new control plane verification failed",
			)
		})

		It("should verify csi-node daemonset is running", func() {
			Expect(helpers.VerifyCSINodeDaemonSetRunning(ctx, k8sClient)).NotTo(
				HaveOccurred(),
				"csi-node daemonset verification failed",
			)
		})

		It("should verify old control plane is stopped", func() {
			Expect(helpers.VerifyOldControlPlaneStopped(ctx, k8sClient)).NotTo(
				HaveOccurred(),
				"old control plane verification failed",
			)
		})
	})
})
