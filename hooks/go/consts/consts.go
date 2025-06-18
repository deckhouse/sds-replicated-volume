/*
Copyright 2022 Flant JSC

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
