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

// HasAnnotation reports whether the object has the given annotation key.
func HasAnnotation(obj metav1.Object, key string) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}

	_, ok := annotations[key]
	return ok
}

// HasAnnotationValue reports whether the object has the given annotation key set to the provided value.
func HasAnnotationValue(obj metav1.Object, key, value string) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}

	return annotations[key] == value
}

// SetAnnotation ensures the object has the given annotation key set to the provided value.
// It returns whether the annotations were changed.
func SetAnnotation(obj metav1.Object, key, value string) (changed bool) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if v, ok := annotations[key]; ok && v == value {
		return false
	}

	annotations[key] = value
	obj.SetAnnotations(annotations)
	return true
}

// RemoveAnnotation removes the given annotation key from the object.
// It returns whether the annotations were changed.
func RemoveAnnotation(obj metav1.Object, key string) (changed bool) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}

	if _, ok := annotations[key]; !ok {
		return false
	}

	delete(annotations, key)
	obj.SetAnnotations(annotations)
	return true
}
