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

package objutilv1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// HasLabel reports whether the object has the given label key.
func HasLabel(obj metav1.Object, key string) bool {
	labels := obj.GetLabels()
	if labels == nil {
		return false
	}

	_, ok := labels[key]
	return ok
}

// HasLabelValue reports whether the object has the given label key set to the provided value.
func HasLabelValue(obj metav1.Object, key, value string) bool {
	labels := obj.GetLabels()
	if labels == nil {
		return false
	}

	return labels[key] == value
}

// SetLabel ensures the object has the given label key set to the provided value.
// It returns whether the labels were changed.
func SetLabel(obj metav1.Object, key, value string) (changed bool) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	if v, ok := labels[key]; ok && v == value {
		return false
	}

	labels[key] = value
	obj.SetLabels(labels)
	return true
}

// RemoveLabel removes the given label key from the object.
// It returns whether the labels were changed.
func RemoveLabel(obj metav1.Object, key string) (changed bool) {
	labels := obj.GetLabels()
	if labels == nil {
		return false
	}

	if _, ok := labels[key]; !ok {
		return false
	}

	delete(labels, key)
	obj.SetLabels(labels)
	return true
}
