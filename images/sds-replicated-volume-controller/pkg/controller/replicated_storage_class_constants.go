package controller

const (
	ReplicatedStorageClassFinalizerName = "replicatedstorageclass.storage.deckhouse.io"

	StorageClassFinalizerName = "storage.deckhouse.io/sds-replicated-volume"
	StorageClassProvisioner   = "replicated.csi.storage.deckhouse.io"
	StorageClassKind          = "StorageClass"
	StorageClassAPIVersion    = "storage.k8s.io/v1"

	ZoneLabel                  = "topology.kubernetes.io/zone"
	StorageClassLabelKeyPrefix = "class.storage.deckhouse.io"

	ReclaimPolicyRetain = "Retain"
	ReclaimPolicyDelete = "Delete"

	ReplicationNone                       = "None"
	ReplicationAvailability               = "Availability"
	ReplicationConsistencyAndAvailability = "ConsistencyAndAvailability"

	TopologyTransZonal = "TransZonal"
	TopologyZonal      = "Zonal"
	TopologyIgnored    = "Ignored"

	StorageClassPlacementCountKey               = "replicated.csi.storage.deckhouse.io/placementCount"
	StorageClassAutoEvictMinReplicaCountKey     = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/AutoEvictMinReplicaCount"
	StorageClassStoragePoolKey                  = "replicated.csi.storage.deckhouse.io/storagePool"
	StorageClassParamReplicasOnDifferentKey     = "replicated.csi.storage.deckhouse.io/replicasOnDifferent"
	StorageClassParamReplicasOnSameKey          = "replicated.csi.storage.deckhouse.io/replicasOnSame"
	StorageClassParamAllowRemoteVolumeAccessKey = "replicated.csi.storage.deckhouse.io/allowRemoteVolumeAccess"

	StorageClassParamFSTypeKey                     = "csi.storage.k8s.io/fstype"
	FsTypeExt4                                     = "ext4"
	StorageClassParamPlacementPolicyKey            = "replicated.csi.storage.deckhouse.io/placementPolicy"
	PlacementPolicyAutoPlaceTopology               = "AutoPlaceTopology"
	StorageClassParamNetProtocolKey                = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Net/protocol"
	NetProtocolC                                   = "C"
	StorageClassParamNetRRConflictKey              = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Net/rr-conflict"
	RrConflictRetryConnect                         = "retry-connect"
	StorageClassParamAutoQuorumKey                 = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-quorum"
	SuspendIo                                      = "suspend-io"
	StorageClassParamAutoAddQuorumTieBreakerKey    = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/auto-add-quorum-tiebreaker"
	StorageClassParamOnNoQuorumKey                 = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-no-quorum"
	StorageClassParamOnNoDataAccessibleKey         = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-no-data-accessible"
	StorageClassParamOnSuspendedPrimaryOutdatedKey = "property.replicated.csi.storage.deckhouse.io/DrbdOptions/Resource/on-suspended-primary-outdated"
	PrimaryOutdatedForceSecondary                  = "force-secondary"

	DefaultStorageClassAnnotationKey = "storageclass.kubernetes.io/is-default-class"
)
