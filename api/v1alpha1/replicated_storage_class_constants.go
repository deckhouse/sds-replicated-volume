package v1alpha1

const (
	ReplicatedStorageClassFinalizerName = "replicatedstorageclass.storage.deckhouse.io"

	StorageClassFinalizerName = "storage.deckhouse.io/sds-replicated-volume"
	StorageClassProvisioner   = "replicated.csi.storage.deckhouse.io"
	StorageClassKind          = "StorageClass"
	StorageClassAPIVersion    = "storage.k8s.io/v1"

	ZoneLabel                  = "topology.kubernetes.io/zone"

	ReclaimPolicyRetain = "Retain"
	ReclaimPolicyDelete = "Delete"

	StorageClassStoragePoolKey = "replicated.csi.storage.deckhouse.io/storagePool"

	DefaultStorageClassAnnotationKey = "storageclass.kubernetes.io/is-default-class"

	ManagedLabelKey   = "storage.deckhouse.io/managed-by"
	ManagedLabelValue = "sds-replicated-volume"

	RSCStorageClassVolumeSnapshotClassAnnotationKey   = "storage.deckhouse.io/volumesnapshotclass"
	RSCStorageClassVolumeSnapshotClassAnnotationValue = "sds-replicated-volume"

	ReplicatedStorageClassParamNameKey = "replicated.csi.storage.deckhouse.io/replicatedStorageClassName"

	StorageClassVirtualizationAnnotationKey   = "virtualdisk.virtualization.deckhouse.io/access-mode"
	StorageClassVirtualizationAnnotationValue = "ReadWriteOnce"
	StorageClassIgnoreLocalAnnotationKey      = "replicatedstorageclass.storage.deckhouse.io/ignore-local"

	ControllerConfigMapName        = "sds-replicated-volume-controller-config"
	VirtualizationModuleEnabledKey = "virtualizationEnabled"

	PodNamespaceEnvVar         = "POD_NAMESPACE"
	ControllerNamespaceDefault = "d8-sds-replicated-volume"
)
