package consts

import "time"

const (
	ModuleURI                = "storage.deckhouse.io/sds-replicated-volume"
	ModuleName               = "sdsReplicatedVolume"
	ModuleLabelValue         = "sds-replicated-volume"
	ModuleNamespace          = "d8-sds-replicated-volume"
	SecretCertExpire30dLabel = ModuleURI + "-cert-expire-in-30d"
)

const (
	DefaultCertExpiredDuration  = time.Hour * 24 * 365
	DefaultCertOutdatedDuration = time.Hour * 24 * 30
)

const (
	ManualCertRenewalPackageName     = "manualcertrenewal"
	ManualCertRenewalPackageURI      = ModuleURI + "-" + ManualCertRenewalPackageName
	ManualCertRenewalInProgressLabel = ManualCertRenewalPackageURI + "-in-progress"
)
