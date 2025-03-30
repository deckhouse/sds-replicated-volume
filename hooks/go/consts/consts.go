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
	Dur365d = time.Hour * 24 * 365
	Dur30d  = time.Hour * 24 * 30
)
