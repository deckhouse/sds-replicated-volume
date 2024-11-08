package controller

import (
	"testing"

	lapi "github.com/LINBIT/golinstor/client"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/storage/v1"
)

func TestLinstorResourcesWatcher(t *testing.T) {
	t.Run("filterNodesByAutoplaceTarget_return_correct_nodes", func(t *testing.T) {
		nodes := []lapi.Node{
			{
				Name: "correct1",
				Props: map[string]string{
					autoplaceTarget: "true",
				},
			},
			{
				Name: "bad",
				Props: map[string]string{
					autoplaceTarget: "false",
				},
			},
			{
				Name:  "correct2",
				Props: map[string]string{},
			},
		}

		expected := []lapi.Node{
			{
				Name: "correct1",
				Props: map[string]string{
					autoplaceTarget: "true",
				},
			},
			{
				Name:  "correct2",
				Props: map[string]string{},
			},
		}

		actual := filterNodesByAutoplaceTarget(nodes)

		assert.ElementsMatch(t, expected, actual)
	})

	t.Run("filterNodesByAutoplaceTarget_return_nothing", func(t *testing.T) {
		nodes := []lapi.Node{
			{
				Name: "bad1",
				Props: map[string]string{
					autoplaceTarget: "false",
				},
			},
			{
				Name: "bad2",
				Props: map[string]string{
					autoplaceTarget: "false",
				},
			},
		}

		actual := filterNodesByAutoplaceTarget(nodes)

		assert.Equal(t, 0, len(actual))
	})

	t.Run("filterNodesByReplicasOnDifferent_returns_correct_nodes", func(t *testing.T) {
		key := "Aux/kubernetes.io/hostname"
		values := []string{"test-host1"}
		nodes := []lapi.Node{
			{
				Props: map[string]string{
					key: "test-host1",
				},
			},
			{
				Props: map[string]string{
					key: "test-host2",
				},
			},
		}
		expected := []lapi.Node{
			{
				Props: map[string]string{
					key: "test-host2",
				},
			},
		}

		actual := filterNodesByReplicasOnDifferent(nodes, key, values)

		assert.ElementsMatch(t, expected, actual)
	})

	t.Run("filterNodesByReplicasOnDifferent_returns_nothing", func(t *testing.T) {
		key := "Aux/kubernetes.io/hostname"
		values := []string{"test-host1", "test-host2"}
		nodes := []lapi.Node{
			{
				Props: map[string]string{
					key: "test-host1",
				},
			},
			{
				Props: map[string]string{
					key: "test-host2",
				},
			},
		}

		actual := filterNodesByReplicasOnDifferent(nodes, key, values)

		assert.Equal(t, 0, len(actual))
	})

	t.Run("getReplicasOnDifferentValues_returns_values", func(t *testing.T) {
		const (
			key       = "Aux/kubernetes.io/hostname"
			testNode1 = "test-node-1"
			testNode2 = "test-node-2"
			testHost1 = "test-host1"
			testHost2 = "test-host2"
		)

		nodes := []lapi.Node{
			{
				Name: testNode1,
				Props: map[string]string{
					key: testHost1,
				},
			},
			{
				Name: testNode2,
				Props: map[string]string{
					key: testHost2,
				},
			},
		}
		resources := []lapi.Resource{
			{
				NodeName: testNode1,
			},
			{
				NodeName: testNode2,
			},
		}
		expected := []string{testHost1, testHost2}

		actual := getReplicasOnDifferentValues(nodes, resources, key)

		assert.ElementsMatch(t, expected, actual)
	})

	t.Run("getReplicasOnDifferentValues_returns_nothing", func(t *testing.T) {
		const (
			key       = "Aux/kubernetes.io/hostname"
			testNode1 = "test-node-1"
			testNode2 = "test-node-2"
			testHost1 = "test-host1"
			testHost2 = "test-host2"
		)

		nodes := []lapi.Node{
			{
				Name: testNode1,
				Props: map[string]string{
					key: testHost1,
				},
			},
			{
				Name: testNode2,
				Props: map[string]string{
					key: testHost2,
				},
			},
		}
		resources := []lapi.Resource{
			{
				NodeName: "testNode3",
			},
			{
				NodeName: "testNode4",
			},
		}

		actual := getReplicasOnDifferentValues(nodes, resources, key)

		assert.Equal(t, 0, len(actual))
	})

	t.Run("filterNodesByReplicasOnSame_returns_correct_nodes", func(t *testing.T) {
		const (
			key       = "Aux/kubernetes.io/hostname"
			testNode1 = "test-node-1"
			testNode2 = "test-node-2"
		)

		nodes := []lapi.Node{
			{
				Name: testNode1,
				Props: map[string]string{
					key: "",
				},
			},
			{
				Name: testNode2,
				Props: map[string]string{
					"another-key": "",
				},
			},
		}
		expected := []lapi.Node{
			{
				Name: testNode1,
				Props: map[string]string{
					key: "",
				},
			},
		}

		actual := filterNodesByReplicasOnSame(nodes, key)

		assert.ElementsMatch(t, expected, actual)
	})

	t.Run("filterNodesByReplicasOnSame_returns_nothing", func(t *testing.T) {
		const (
			key       = "Aux/kubernetes.io/hostname"
			testNode1 = "test-node-1"
			testNode2 = "test-node-2"
		)

		nodes := []lapi.Node{
			{
				Name: testNode1,
				Props: map[string]string{
					"other-key": "",
				},
			},
			{
				Name: testNode2,
				Props: map[string]string{
					"another-key": "",
				},
			},
		}

		actual := filterNodesByReplicasOnSame(nodes, key)

		assert.Equal(t, 0, len(actual))
	})

	t.Run("getResourceGroupByResource_returns_RG", func(t *testing.T) {
		const (
			rdName = "test-rd"
			rgName = "test-rg"
		)
		rds := map[string]lapi.ResourceDefinitionWithVolumeDefinition{
			rdName: {
				ResourceDefinition: lapi.ResourceDefinition{ResourceGroupName: rgName},
			},
		}

		rgs := map[string]lapi.ResourceGroup{
			rgName: {
				Name:        rgName,
				Description: "CORRECT ONE",
			},
		}

		expected := lapi.ResourceGroup{
			Name:        rgName,
			Description: "CORRECT ONE",
		}

		actual := getResourceGroupByResource(rdName, rds, rgs)

		assert.Equal(t, expected, actual)
	})

	t.Run("getResourceGroupByResource_returns_nothing", func(t *testing.T) {
		const (
			rdName = "test-rd"
			rgName = "test-rg"
		)
		rds := map[string]lapi.ResourceDefinitionWithVolumeDefinition{
			rdName: {
				ResourceDefinition: lapi.ResourceDefinition{ResourceGroupName: rgName},
			},
		}

		rgs := map[string]lapi.ResourceGroup{
			"another-name": {
				Name:        rgName,
				Description: "CORRECT ONE",
			},
		}

		actual := getResourceGroupByResource(rdName, rds, rgs)

		assert.Equal(t, lapi.ResourceGroup{}, actual)
	})

	t.Run("filterNodesByUsed_returns_nodes", func(t *testing.T) {
		const (
			testNode1 = "test-node-1"
			testNode2 = "test-node-2"
		)

		nodes := []lapi.Node{
			{
				Name: testNode1,
			},
			{
				Name: testNode2,
			},
		}

		resources := []lapi.Resource{
			{
				NodeName: testNode1,
			},
		}

		expected := []lapi.Node{
			{
				Name: testNode2,
			},
		}

		actual := filterOutUsedNodes(nodes, resources)
		assert.ElementsMatch(t, expected, actual)
	})

	t.Run("filterNodesByUsed_returns_nothing", func(t *testing.T) {
		const (
			testNode1 = "test-node-1"
			testNode2 = "test-node-2"
		)

		nodes := []lapi.Node{
			{
				Name: testNode1,
			},
			{
				Name: testNode2,
			},
		}

		resources := []lapi.Resource{
			{
				NodeName: testNode1,
			},
			{
				NodeName: testNode2,
			},
		}

		actual := filterOutUsedNodes(nodes, resources)
		assert.Equal(t, 0, len(actual))
	})

	t.Run("hasDisklessReplica_returns_true", func(t *testing.T) {
		resources := []lapi.Resource{
			{
				Flags: disklessFlags,
			},
			{
				Flags: []string{},
			},
		}

		has := hasDisklessReplica(resources)
		assert.True(t, has)
	})

	t.Run("hasDisklessReplica_returns_false", func(t *testing.T) {
		resources := []lapi.Resource{
			{
				Flags: []string{},
			},
			{
				Flags: []string{},
			},
		}

		has := hasDisklessReplica(resources)
		assert.False(t, has)
	})

	t.Run("removePrefixes_removes_correctly", func(t *testing.T) {
		testParams := map[string]string{
			"test/auto-quorum":                   "test-auto-quorum",
			"test/on-no-data-accessible":         "test-on-no-data-accessible",
			"test/on-suspended-primary-outdated": "test-on-suspended-primary-outdated",
			"test/rr-conflict":                   "test-rr-conflict",
			replicasOnSameSCKey:                  "test-replicas-on-same",
			replicasOnDifferentSCKey:             "not-the-same",
			placementCountSCKey:                  "3",
			storagePoolSCKey:                     "not-the-same",
		}

		expected := map[string]string{
			"auto-quorum":                   "test-auto-quorum",
			"on-no-data-accessible":         "test-on-no-data-accessible",
			"on-suspended-primary-outdated": "test-on-suspended-primary-outdated",
			"rr-conflict":                   "test-rr-conflict",
			replicasOnSameSCKey:             "test-replicas-on-same",
			replicasOnDifferentSCKey:        "not-the-same",
			placementCountSCKey:             "3",
			storagePoolSCKey:                "not-the-same",
		}

		actual := removePrefixes(testParams)

		assert.Equal(t, expected, actual)
	})

	t.Run("getRGReplicasValue_returns_value", func(t *testing.T) {
		values := []string{
			"test/another/real/value",
			"test/real/value",
			"real/value",
		}
		expected := "real/value"

		for _, v := range values {
			actual := getRGReplicasValue(v)
			assert.Equal(t, expected, actual)
		}
	})

	t.Run("getMissMatchedParams_returns_nothing", func(t *testing.T) {
		testParams := map[string]string{
			"test/auto-quorum":                   "test-auto-quorum",
			"test/on-no-data-accessible":         "test-on-no-data-accessible",
			"test/on-suspended-primary-outdated": "test-on-suspended-primary-outdated",
			"test/rr-conflict":                   "test-rr-conflict",
			replicasOnSameSCKey:                  "test-replicas-on-same",
			replicasOnDifferentSCKey:             "test-replicas-on-diff",
			placementCountSCKey:                  "3",
			storagePoolSCKey:                     "test-sp",
		}
		sc := v1.StorageClass{Parameters: testParams}
		rg := lapi.ResourceGroup{
			Props: map[string]string{"test/auto-quorum": "test-auto-quorum",
				"test/on-no-data-accessible":         "test-on-no-data-accessible",
				"test/on-suspended-primary-outdated": "test-on-suspended-primary-outdated",
				"test/rr-conflict":                   "test-rr-conflict"},
			SelectFilter: lapi.AutoSelectFilter{
				ReplicasOnSame:      []string{"test-replicas-on-same"},
				ReplicasOnDifferent: []string{"test-replicas-on-diff"},
				PlaceCount:          3,
				StoragePool:         "test-sp",
			},
		}

		diff := getMissMatchedParams(sc, rg)

		assert.Equal(t, 0, len(diff))
	})

	t.Run("getMissMatchedParams_returns_missMatchedParams", func(t *testing.T) {
		testParams := map[string]string{
			"test/auto-quorum":                   "test-auto-quorum",
			"test/on-no-data-accessible":         "test-on-no-data-accessible",
			"test/on-suspended-primary-outdated": "test-on-suspended-primary-outdated",
			"test/rr-conflict":                   "test-rr-conflict",
			replicasOnSameSCKey:                  "test-replicas-on-same",
			replicasOnDifferentSCKey:             "not-the-same",
			placementCountSCKey:                  "3",
			storagePoolSCKey:                     "not-the-same",
		}
		sc := v1.StorageClass{Parameters: testParams}
		rg := lapi.ResourceGroup{
			Props: map[string]string{"test/auto-quorum": "test-auto-quorum",
				"test/on-no-data-accessible":         "test-on-no-data-accessible",
				"test/on-suspended-primary-outdated": "test-on-suspended-primary-outdated",
				"test/rr-conflict":                   "test-rr-conflict"},
			SelectFilter: lapi.AutoSelectFilter{
				ReplicasOnSame:      []string{"test-replicas-on-same"},
				ReplicasOnDifferent: []string{"test-replicas-on-diff"},
				PlaceCount:          3,
				StoragePool:         "test-sp",
			},
		}

		expectedDiff := []string{replicasOnDifferentSCKey, storagePoolSCKey}

		actualDiff := getMissMatchedParams(sc, rg)

		assert.ElementsMatch(t, expectedDiff, actualDiff)
	})
}
