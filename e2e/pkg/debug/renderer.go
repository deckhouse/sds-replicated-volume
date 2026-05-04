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

package debug

// CleanObj removes noisy metadata fields (managedFields, resourceVersion, uid,
// creationTimestamp, last-applied-configuration annotation) from a Kubernetes
// object to produce a cleaner diff.
func CleanObj(obj map[string]any) {
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return
	}
	delete(meta, "managedFields")
	delete(meta, "resourceVersion")
	delete(meta, "uid")
	delete(meta, "creationTimestamp")

	ann, _ := meta["annotations"].(map[string]any)
	delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
	if len(ann) == 0 {
		delete(meta, "annotations")
	}
}

// PrettyLines cleans a Kubernetes object and returns its YAML representation
// as a slice of lines.
func PrettyLines(obj map[string]any) []string {
	CleanObj(obj)
	return YAMLLines(obj)
}
