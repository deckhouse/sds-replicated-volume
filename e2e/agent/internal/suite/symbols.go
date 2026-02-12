package suite

import (
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/testing"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Suite struct {
	ZClient           testing.Zymbol[client.Client]
	SelectedNodeNames testing.Symbol[[]string]
	Client            testing.Symbol[client.Client]
}

// Provides at least one node name, all names are unique
var SymbolSelectedNodeNames testing.Symbol[[]string]

// Provides client, which is authorized in the cluster
var SymbolClient testing.Symbol[client.Client]

// Provides exactly one [corev1.Node] for each [SymbolSelectedNodeNames]
// Indexing is the same as in [SymbolSelectedNodeNames].
var SymbolSelectedNodes testing.Symbol[[]corev1.Node]

// Provides exactly one [snc.LVMVolumeGroup] for each [corev1.Node] from
// [SymbolSelectedNodes].
// Indexing is the same as in [SymbolSelectedNodes].
var SymbolSelectedLVGs testing.Symbol[[]snc.LVMVolumeGroup]
