package consts

import "time"

const (
	ModuleUri                = "storage.deckhouse.io/sds-replicated-volume"
	ModuleName               = "sdsReplicatedVolume"
	ModuleLabelValue         = "sds-replicated-volume"
	ModuleNamespace          = "d8-sds-replicated-volume"
	SecretCertExpire30dLabel = ModuleUri + "-cert-expire-in-30d"
)

const (
	DefaultCertExpiredDuration  = time.Hour * 24 * 365
	DefaultCertOutdatedDuration = time.Hour * 24 * 30
)

const (
	ManualCertRenewalPackageName     = "manualcertrenewal"
	ManualCertRenewalPackageUri      = ModuleUri + "-" + ManualCertRenewalPackageName
	ManualCertRenewalInProgressLabel = ManualCertRenewalPackageUri + "-in-progress"
)
