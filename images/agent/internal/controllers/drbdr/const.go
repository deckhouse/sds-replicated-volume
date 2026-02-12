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

package drbdr

import (
	"strings"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	// ControllerName is the stable name for the DRBD controller.
	// Used in .Named(...) for controller-runtime builder.
	ControllerName = "drbdr-controller"

	// ScannerName is the name of the DRBD scanner component.
	ScannerName = "drbd-scanner"

	// drbdNamePrefix is the prefix used for standard DRBD resource names.
	drbdNamePrefix = "sdsrv-"
)

// DRBDNameFromK8SName returns the standard DRBD resource name for a K8S name.
func DRBDNameFromK8SName(k8sName string) string {
	return drbdNamePrefix + k8sName
}

// DRBDResourceNameOnTheNode returns the DRBD resource name to use on the node.
// If ActualNameOnTheNode is set, it returns that; otherwise returns the standard name.
func DRBDResourceNameOnTheNode(drbdr *v1alpha1.DRBDResource) string {
	if drbdr.Spec.ActualNameOnTheNode != "" {
		return drbdr.Spec.ActualNameOnTheNode
	}
	return DRBDNameFromK8SName(drbdr.Name)
}

// ParseDRBDResourceNameOnTheNode extracts the K8S name from a standard DRBD resource name.
// Returns the K8S name and true if the name has the standard prefix, or the original name and false otherwise.
func ParseDRBDResourceNameOnTheNode(s string) (string, bool) {
	return strings.CutPrefix(s, drbdNamePrefix)
}
