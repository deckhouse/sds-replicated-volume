package driver

import (
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var parameterStoragePoolName = "replicated.csi.storage.deckhouse.io/storagePool"


func NewDRBDCluster(clusterName string) srv.DRBDCluster {
	typeMeta := metav1.TypeMeta{
		Kind:       "DRBDCluster",
		APIVersion: "v1alpha1",
	}

	objectMeta := metav1.ObjectMeta{
		Name: clusterName,
	}

	attachmentRequested := []string{""}

	topologySpreadConstraints := []srv.TopologySpreadConstraint{
		{
			MaxSkew:           1,
			TopologyKey:       "zone",
			WhenUnsatisfiable: "DoNotSchedule",
		},
	}

	affinity := srv.Affinity{
		NodeAffinity: srv.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: srv.NodeSelector{
				NodeSelectorTerms: []srv.NodeSelectorTerm{
					{
						MatchExpressions: []srv.SelectorRequirement{
							{
								Key:      "kubernetes.io/e2e-az-name",
								Operator: "In",
								Values:   []string{"e2e-az1", "e2e-az2"},
							},
						},
					},
				},
			},
		},
	}

	autoDiskful := srv.AutoDiskful{
		DelaySeconds: 30,
	}

	autoRecovery := srv.AutoRecovery{
		DelaySeconds: 60,
	}

	storagePoolSelector := []srv.LabelSelector{
		{
			MatchExpressions: []srv.SelectorRequirement{
				{
					Key:      parameterStoragePoolName,
					Operator: "In",
					Values:   []string{""},
				},
			},
		},
	}

	spec := srv.DRBDClusterSpec{
		Replicas:                  3,
		QuorumPolicy:              "majority",
		NetworkPoolName:           "default-network-pool",
		SharedSecret:              "secure-secret",
		Size:                      int64(1),
		DrbdCurrentGi:             "1",
		Port:                      7789,
		Minor:                     1,
		AttachmentRequested:       attachmentRequested,
		TopologySpreadConstraints: topologySpreadConstraints,
		Affinity:                  affinity,
		AutoDiskful:               autoDiskful,
		AutoRecovery:              autoRecovery,
		StoragePoolSelector:       storagePoolSelector,
	}

	status := srv.DRBDClusterStatus{
		Size: 1024,
		AttachmentCompleted: []string{
			"attachment1",
		},
		Conditions: []metav1.Condition{
			{
				Type:   "Ready",
				Status: "True",
				Reason: "ClusterIsReady",
			},
		},
	}

	return srv.DRBDCluster{
		TypeMeta:   typeMeta,
		ObjectMeta: objectMeta,
		Spec:       spec,
		Status:     status,
	}
}