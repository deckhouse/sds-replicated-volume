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

package rvrfinalizerrelease_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func TestRvrGCController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrGCController Suite")
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

// InterceptRVRGet builds interceptor.Funcs that applies intercept() only for
// Get calls of ReplicatedVolumeReplica objects. All other Get calls are passed
// through to the underlying client unchanged. List calls are not intercepted.
func InterceptRVRGet(
	intercept func(*v1alpha3.ReplicatedVolumeReplica) error,
) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica)
			if !ok {
				return cl.Get(ctx, key, obj, opts...)
			}
			return intercept(rvr)
		},
	}
}
