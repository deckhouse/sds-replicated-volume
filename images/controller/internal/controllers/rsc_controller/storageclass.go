package rsccontroller

const (
	storageClassProvisioner = "replicated.csi.storage.deckhouse.io"

	storageClassKind       = "StorageClass"
	storageClassAPIVersion = "storage.k8s.io/v1"

	storageClassFinalizerName = "storage.deckhouse.io/sds-replicated-volume"

	managedLabelKey   = "storage.deckhouse.io/managed-by"
	managedLabelValue = "sds-replicated-volume"

	rscStorageClassVolumeSnapshotClassAnnotationKey   = "storage.deckhouse.io/volumesnapshotclass"
	rscStorageClassVolumeSnapshotClassAnnotationValue = "sds-replicated-volume"

	storageClassPlacementCountKey           = "replicated.csi.storage.deckhouse.io/placementCount"
	storageClassAutoEvictMinReplicaCountKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/AutoEvictMinReplicaCount"
	storageClassStoragePoolKey              = "replicated.csi.storage.deckhouse.io/storagePool"

	storageClassParamReplicasOnDifferentKey = "replicated.csi.storage.deckhouse.io/replicasOnDifferent"
	storageClassParamReplicasOnSameKey      = "replicated.csi.storage.deckhouse.io/replicasOnSame"

	storageClassParamAllowRemoteVolumeAccessKey   = "replicated.csi.storage.deckhouse.io/allowRemoteVolumeAccess"
	storageClassParamAllowRemoteVolumeAccessValue = "- fromSame:\n  - topology.kubernetes.io/zone"

	replicatedStorageClassParamNameKey = "replicated.csi.storage.deckhouse.io/replicatedStorageClassName"

	storageClassParamTopologyKey = "replicated.csi.storage.deckhouse.io/topology"
	storageClassParamZonesKey    = "replicated.csi.storage.deckhouse.io/zones"

	storageClassParamFSTypeKey = "csi.storage.k8s.io/fstype"
	fsTypeExt4                 = "ext4"

	storageClassParamPlacementPolicyKey         = "replicated.csi.storage.deckhouse.io/placementPolicy"
	placementPolicyAutoPlaceTopology            = "AutoPlaceTopology"
	storageClassParamNetProtocolKey             = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Net/protocol"
	netProtocolC                                = "C"
	storageClassParamNetRRConflictKey           = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Net/rr-conflict"
	rrConflictRetryConnect                      = "retry-connect"
	storageClassParamAutoQuorumKey              = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-quorum"
	suspendIo                                   = "suspend-io"
	storageClassParamAutoAddQuorumTieBreakerKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-add-quorum-tiebreaker"

	storageClassParamOnNoQuorumKey                 = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-no-quorum"
	storageClassParamOnNoDataAccessibleKey         = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-no-data-accessible"
	storageClassParamOnSuspendedPrimaryOutdatedKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-suspended-primary-outdated"
	primaryOutdatedForceSecondary                  = "force-secondary"

	storageClassParamAutoDiskfulKey             = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-diskful"
	storageClassParamAutoDiskfulAllowCleanupKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-diskful-allow-cleanup"

	quorumMinimumRedundancyWithPrefixSCKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/quorum-minimum-redundancy"

	zoneLabel                  = "topology.kubernetes.io/zone"
	storageClassLabelKeyPrefix = "class.storage.deckhouse.io"

	storageClassVirtualizationAnnotationKey   = "virtualdisk.virtualization.deckhouse.io/access-mode"
	storageClassVirtualizationAnnotationValue = "ReadWriteOnce"
	storageClassIgnoreLocalAnnotationKey      = "replicatedstorageclass.storage.deckhouse.io/ignore-local"

	controllerConfigMapName        = "sds-replicated-volume-controller-config"
	virtualizationModuleEnabledKey = "virtualizationEnabled"

	podNamespaceEnvVar         = "POD_NAMESPACE"
	controllerNamespaceDefault = "d8-sds-replicated-volume"
)
