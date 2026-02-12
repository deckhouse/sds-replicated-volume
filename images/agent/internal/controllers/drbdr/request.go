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

// DRBDReconcileRequest represents a reconciliation request for a DRBD resource.
// Exactly one of Name or ActualNameOnTheNode should be set:
//   - Name: set for K8S-originated events (watch on DRBDResource) and scanner events for prefixed DRBD names
//   - ActualNameOnTheNode: set for scanner-originated events for non-prefixed DRBD names (orphan/rename handling)
type DRBDReconcileRequest struct {
	// Name is the K8S resource name. Set for K8S-originated events and scanner events
	// where the DRBD name has the standard "sdsrv-" prefix.
	Name string
	// ActualNameOnTheNode is the DRBD resource name as observed on the node.
	// Set for scanner-originated events where the DRBD name does not have the standard prefix.
	ActualNameOnTheNode string
}
