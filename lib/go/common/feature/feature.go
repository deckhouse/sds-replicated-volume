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

// Package feature tells which edition-specific features the binary is built with.
//
// The edition is the Go build tag the images are built with (`go build -tags
// $MODULE_EDITION`, see images/*/werf.inc.yaml and .gitlab-ci.yml): ce, se, seplus,
// ee or csepro. A build without any of them gets the feature set of the editions
// that lack the feature, so a binary built by hand never promises what the
// cluster's sds-node-configurator may not do.
package feature

// VolumeCleanupEnabled reports whether backing volumes may be wiped before
// removal (LVMLogicalVolume spec.volumeCleanup). The wipe is done by the agent of
// sds-node-configurator, which supports it in the same editions only; elsewhere it
// fails the deletion of every LVMLogicalVolume that asks for it.
func VolumeCleanupEnabled() bool {
	return volumeCleanupEnabled
}
