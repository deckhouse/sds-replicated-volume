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

package capabilities

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-common-lib/cooldown"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Reporter publishes this agent's kernel capability on its own Pod once.
// It only writes through the client, so it never starts a Pod informer.
type Reporter struct {
	writer    client.Writer
	pod       client.ObjectKey
	supported bool
}

var _ manager.Runnable = (*Reporter)(nil)

func NewReporter(writer client.Writer, pod client.ObjectKey, supported bool) *Reporter {
	return &Reporter{writer: writer, pod: pod, supported: supported}
}

// Start retries publication until success or cancellation. Returning nil after
// success finishes this runnable without stopping the controller-runtime manager.
func (r *Reporter) Start(ctx context.Context) error {
	patch := client.RawPatch(types.MergePatchType, []byte(fmt.Sprintf(
		`{"metadata":{"annotations":{%q:%q}}}`,
		v1alpha1.AgentShadowDiskfulSupportedAnnotationKey, fmt.Sprint(r.supported),
	)))
	cd := cooldown.NewExponentialCooldown(time.Second, time.Minute)
	for cd.Hit(ctx) == nil {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: r.pod.Name, Namespace: r.pod.Namespace}}
		if err := r.writer.Patch(ctx, pod, patch); err == nil {
			return nil
		} else if ctx.Err() == nil {
			log.FromContext(ctx).Error(err, "publishing agent ShadowDiskful support", "pod", r.pod)
		}
	}
	return nil
}
