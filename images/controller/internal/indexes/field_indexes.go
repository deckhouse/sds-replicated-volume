/*
Copyright 2025 Flant JSC

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

package indexes

const (
	// IndexFieldRVAByReplicatedVolumeName is a controller-runtime cache index field name
	// used to quickly list ReplicatedVolumeAttachment objects belonging to a specific RV.
	//
	// NOTE: this is not a JSONPath; it must match the field name used with:
	// - mgr.GetFieldIndexer().IndexField(...)
	// - client.MatchingFields{...}
	// - fake.ClientBuilder.WithIndex(...)
	IndexFieldRVAByReplicatedVolumeName = "spec.replicatedVolumeName"

	// IndexFieldRVRByReplicatedVolumeName is a controller-runtime cache index field name
	// used to quickly list ReplicatedVolumeReplica objects belonging to a specific RV.
	//
	// NOTE: this is not a JSONPath; it must match the field name used with:
	// - mgr.GetFieldIndexer().IndexField(...)
	// - client.MatchingFields{...}
	// - fake.ClientBuilder.WithIndex(...)
	IndexFieldRVRByReplicatedVolumeName = "spec.replicatedVolumeName"
)
