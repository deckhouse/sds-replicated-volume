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

package kubetesting

import (
	"bytes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// NodeExec provides the ability to execute commands inside agent pods on
// specific cluster nodes. Call Exec with the full command (e.g. drbdmeta
// subcommands); the test scope's context is used (during cleanup, the scope
// provides context.Background() so cleanups can run without cancellation).
type NodeExec struct {
	cs            *kubernetes.Clientset
	cfg           *rest.Config
	namespace     string
	labelSelector string
	container     string
}

// DiscoverNodeExec creates a NodeExec using the cluster kubeconfig and the
// agent pod selector from PodLogMonitorOptions.
func DiscoverNodeExec(e envtesting.E, cs *kubernetes.Clientset, podOpts PodLogMonitorOptions) *NodeExec {
	cfg, err := config.GetConfig()
	if err != nil {
		e.Fatalf("discover: getting kubeconfig for NodeExec: %v", err)
	}
	return &NodeExec{
		cs:            cs,
		cfg:           cfg,
		namespace:     podOpts.Namespace,
		labelSelector: podOpts.LabelSelector,
		container:     podOpts.Container,
	}
}

// Exec runs a command inside the agent pod on the given node and returns
// its combined stdout. Uses e.Context() (during cleanup the scope supplies
// context.Background()). Fails the test on error.
func (ne *NodeExec) Exec(e envtesting.E, nodeName string, cmd ...string) string {
	ctx := e.Context()
	podName := ne.findPodOnNode(e, nodeName)

	req := ne.cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(ne.namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: ne.container,
		Command:   cmd,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(ne.cfg, "POST", req.URL())
	if err != nil {
		e.Fatalf("creating executor for pod %q on node %q: %v", podName, nodeName, err)
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		e.Fatalf("exec in pod %q on node %q (cmd: %v): %v\nstdout: %s\nstderr: %s",
			podName, nodeName, cmd, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func (ne *NodeExec) findPodOnNode(e envtesting.E, nodeName string) string {
	ctx := e.Context()
	pods, err := ne.cs.CoreV1().Pods(ne.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: ne.labelSelector,
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		e.Fatalf("listing agent pods on node %q: %v", nodeName, err)
	}
	if len(pods.Items) != 1 {
		e.Fatalf("expected 1 agent pod on node %q, got %d", nodeName, len(pods.Items))
	}
	return pods.Items[0].Name
}
