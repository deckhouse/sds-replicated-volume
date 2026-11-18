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

package sdnavailability

import (
	"fmt"

	sdn "github.com/deckhouse/sdn/api/network.deckhouse.io/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Enabled fixes effective SDN availability for this process before any SDN informer is registered.
func Enabled(requested bool, mapper meta.RESTMapper, log logr.Logger) (bool, error) {
	if !requested {
		return false, nil
	}
	neededKinds := []schema.GroupVersionKind{sdn.NodeNetworkStatusGVK, sdn.SystemNetworkGVK}
	for _, kind := range neededKinds {
		if _, err := mapper.RESTMapping(kind.GroupKind(), kind.Version); err != nil {
			if !meta.IsNoMatchError(err) {
				return false, fmt.Errorf("discovering required SDN Kind %s: %w", kind.String(), err)
			}
			log.Info("SDN watches and reads disabled until restart: required Kind unavailable", "kind", kind.String(), "reason", err.Error())
			return false, nil
		}
	}
	return true, nil
}
