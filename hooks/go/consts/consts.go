package consts

import "time"

const (
	ModuleURI                = "storage.deckhouse.io/sds-replicated-volume"
	ModuleName               = "sdsReplicatedVolume"
	ModuleLabelValue         = "sds-replicated-volume"
	ModuleNamespace          = "d8-sds-replicated-volume"
	SecretCertExpire30dLabel = ModuleURI + "-cert-expire-in-30d"
	// this is used to avoid trigger certificate hooks
	SecretCertHookSuppressedByLabel = ModuleURI + "-cert-hook-suppressed-by"
)

const (
	DefaultCertExpiredDuration  = time.Hour * 24 * 365
	DefaultCertOutdatedDuration = time.Hour * 24 * 30
)
