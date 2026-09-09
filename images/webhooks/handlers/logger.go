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

package handlers

import "github.com/deckhouse/sds-common-lib/logger"

// log is what the validators report through. It is a package variable and not a
// parameter because the validators are plain functions passed to kubewebhook,
// whose signature has no room for one; the alternative -- turning each of them
// into a method on a struct that carries the logger -- would change the exported
// API of this package and every test that calls a validator directly.
//
// It starts as a no-op so a test that does not set it stays quiet, and main
// replaces it with the process logger before the first request is served. The
// validators used to write to klog, which ignored the module's log level and
// wrote text where everything else in the pod writes JSON.
var log = logger.NewNop()

// SetLogger points the validators at the process logger. Call it before serving.
func SetLogger(l logger.Logger) {
	log = l
}
