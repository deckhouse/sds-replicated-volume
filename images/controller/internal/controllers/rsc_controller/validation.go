package rsccontroller

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func validateRSCSpec(rsc *v1alpha1.ReplicatedStorageClass, clusterZones map[string]struct{}) (bool, string) {
	var (
		b     strings.Builder
		valid = true
	)

	b.WriteString("Validation of ReplicatedStorageClass failed: ")

	if rsc.Spec.StoragePool == "" {
		valid = false
		b.WriteString("StoragePool is empty; ")
	}

	if rsc.Spec.ReclaimPolicy == "" {
		valid = false
		b.WriteString("ReclaimPolicy is empty; ")
	}

	switch rsc.Spec.Topology {
	case v1alpha1.RSCTopologyTransZonal:
		if len(rsc.Spec.Zones) == 0 {
			valid = false
			b.WriteString("Topology is set to 'TransZonal', but zones are not specified; ")
		} else {
			switch rsc.Spec.Replication {
			case v1alpha1.ReplicationAvailability, v1alpha1.ReplicationConsistencyAndAvailability:
				if len(rsc.Spec.Zones) != 3 {
					valid = false
					b.WriteString(fmt.Sprintf(
						"Selected unacceptable amount of zones for replication type: %s; correct number of zones should be 3; ",
						rsc.Spec.Replication,
					))
				}
			case v1alpha1.ReplicationNone:
			default:
				valid = false
				b.WriteString(fmt.Sprintf("Selected unsupported replication type: %s; ", rsc.Spec.Replication))
			}
		}

	case v1alpha1.RSCTopologyZonal:
		if len(rsc.Spec.Zones) != 0 {
			valid = false
			b.WriteString("Topology is set to 'Zonal', but zones are specified; ")
		}

	case v1alpha1.RSCTopologyIgnored:
		if len(clusterZones) > 0 {
			valid = false
			b.WriteString("Setting 'topology' to 'Ignored' is prohibited when zones are present in the cluster; ")
		}
		if len(rsc.Spec.Zones) != 0 {
			valid = false
			b.WriteString("Topology is set to 'Ignored', but zones are specified; ")
		}

	default:
		valid = false
		b.WriteString(fmt.Sprintf("Selected unsupported topology: %s; ", rsc.Spec.Topology))
	}

	return valid, b.String()
}

func computeClusterZones(nodes []corev1.Node) map[string]struct{} {
	zones := make(map[string]struct{})
	for i := range nodes {
		node := &nodes[i]
		if zone, ok := node.Labels[corev1.LabelTopologyZone]; ok {
			zones[zone] = struct{}{}
		}
	}
	return zones
}
