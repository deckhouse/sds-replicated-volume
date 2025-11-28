// cspell:words Diskless Logr Subresource apimachinery gomega gvks metav onsi

package rvr_status_config_peers_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_peers"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)
	return scheme
}

func ownerRef(scheme *runtime.Scheme, rv *v1alpha3.ReplicatedVolume) metav1.OwnerReference {
	gvks, _, _ := scheme.ObjectKinds(rv)
	var apiVersion, kind string
	if len(gvks) > 0 {
		apiVersion = gvks[0].GroupVersion().String()
		kind = gvks[0].Kind
	}
	return metav1.OwnerReference{
		APIVersion: apiVersion,
		Kind:       kind,
		Name:       rv.Name,
		UID:        rv.UID,
		Controller: func() *bool { b := true; return &b }(),
	}
}

// HaveNoPeers is a Gomega matcher that checks a single RVR has no peers
func HaveNoPeers() gomegatypes.GomegaMatcher {
	return SatisfyAny(
		WithTransform(func(rvr v1alpha3.ReplicatedVolumeReplica) *v1alpha3.ReplicatedVolumeReplicaStatus {
			return rvr.Status
		}, BeNil()),
		WithTransform(func(rvr v1alpha3.ReplicatedVolumeReplica) *v1alpha3.DRBDConfig {
			if rvr.Status == nil {
				return nil
			}
			return rvr.Status.Config
		}, BeNil()),
		SatisfyAll(
			WithTransform(func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
				return rvr.Status != nil && rvr.Status.Config != nil
			}, BeTrue()),
			WithTransform(func(rvr v1alpha3.ReplicatedVolumeReplica) map[string]v1alpha3.Peer {
				if rvr.Status == nil || rvr.Status.Config == nil {
					return nil
				}
				return rvr.Status.Config.Peers
			}, BeEmpty()),
		),
	)
}

// HaveAllPeersSet is a matcher factory that returns a Gomega matcher for a single RVR
// It checks that the RVR has all other RVRs from expectedResources as peers
func HaveAllPeersSet(expectedResources []v1alpha3.ReplicatedVolumeReplica) gomegatypes.GomegaMatcher {
	return SatisfyAll(
		HaveField("Status.Config.Peers", Not(BeNil())),
		Satisfy(func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
			_, hasSelf := rvr.Status.Config.Peers[rvr.Spec.NodeName]
			return !hasSelf
		}),
		HaveField("Status.Config.Peers", HaveLen(len(expectedResources)-1)),
		WithTransform(func(rvr v1alpha3.ReplicatedVolumeReplica) []string {
			if rvr.Status == nil || rvr.Status.Config == nil {
				return []string{"RVR has nil Status or Status.Config"}
			}
			var failures []string
			for j := range expectedResources {
				if expectedResources[j].Spec.NodeName == rvr.Spec.NodeName {
					continue // Skip self
				}
				if expectedResources[j].Status == nil {
					failures = append(failures, fmt.Sprintf("expected peer RVR %s has nil Status", expectedResources[j].Name))
					continue
				}
				if expectedResources[j].Status.Config == nil {
					failures = append(failures, fmt.Sprintf("expected peer RVR %s has nil Status.Config", expectedResources[j].Name))
					continue
				}
				if expectedResources[j].Status.Config.NodeId == nil {
					failures = append(failures, fmt.Sprintf("expected peer RVR %s has nil NodeId", expectedResources[j].Name))
					continue
				}
				if expectedResources[j].Status.Config.Address == nil {
					failures = append(failures, fmt.Sprintf("expected peer RVR %s has nil Address", expectedResources[j].Name))
					continue
				}
				expectedPeer := v1alpha3.Peer{
					NodeId:   *expectedResources[j].Status.Config.NodeId,
					Address:  *expectedResources[j].Status.Config.Address,
					Diskless: expectedResources[j].Spec.Diskless,
				}
				actualPeer, hasPeer := rvr.Status.Config.Peers[expectedResources[j].Spec.NodeName]
				if !hasPeer {
					failures = append(failures, fmt.Sprintf("missing peer for node %s", expectedResources[j].Spec.NodeName))
					continue
				}
				if actualPeer.NodeId != expectedPeer.NodeId {
					failures = append(failures, fmt.Sprintf("peer %s has wrong NodeId: got %d, expected %d", expectedResources[j].Spec.NodeName, actualPeer.NodeId, expectedPeer.NodeId))
				}
				if actualPeer.Address != expectedPeer.Address {
					failures = append(failures, fmt.Sprintf("peer %s has wrong Address: got %v, expected %v", expectedResources[j].Spec.NodeName, actualPeer.Address, expectedPeer.Address))
				}
				if actualPeer.Diskless != expectedPeer.Diskless {
					failures = append(failures, fmt.Sprintf("peer %s has wrong Diskless: got %v, expected %v", expectedResources[j].Spec.NodeName, actualPeer.Diskless, expectedPeer.Diskless))
				}
			}
			return failures
		}, BeEmpty()),
	)
}

// HaveAllPeersSetForAll is a Gomega matcher that checks all RVRs in a list have all peers set
func HaveAllPeersSetForAll() gomegatypes.GomegaMatcher {
	return WithTransform(func(rvrList []v1alpha3.ReplicatedVolumeReplica) bool {
		matcher := HaveEach(HaveAllPeersSet(rvrList))
		success, _ := matcher.Match(rvrList)
		return success
	}, BeTrue())
}

// createResources is a generic helper to create Kubernetes resources
func createResources[T any](ctx context.Context, cl client.Client, resources []T, setup func(*T)) error {
	var err error
	for i := range resources {
		if setup != nil {
			setup(&resources[i])
		}
		obj := any(&resources[i]).(client.Object)
		err = errors.Join(cl.Create(ctx, obj))
	}
	return err
}

// whenResourcesCreatedVars holds the created resources
type whenResourcesCreatedVars[T any] struct {
	Resources []T
}

// whenResourcesCreated is a generic helper to create resources in a test context
func whenResourcesCreated[T any](desc string, cl *client.Client, resources []T, fn func(vars *whenResourcesCreatedVars[T]), setup func(*T), args ...any) {
	When(desc, args, func() {
		var vars whenResourcesCreatedVars[T]

		BeforeEach(func() {
			vars.Resources = make([]T, len(resources))
			for i := range resources {
				// Use DeepCopy if available
				src := any(&resources[i]).(client.Object)
				if copier, ok := src.(interface{ DeepCopyInto(client.Object) }); ok {
					dst := any(&vars.Resources[i]).(client.Object)
					copier.DeepCopyInto(dst)
				} else {
					vars.Resources[i] = resources[i]
				}
			}
			Expect(vars.Resources).To(HaveLen(len(resources)))
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(createResources(ctx, *cl, vars.Resources, setup)).To(Succeed())
		})

		fn(&vars)
	})
}

func Get[T any](ctx context.Context, cl client.Client, key types.NamespacedName, opts ...client.GetOption) (T, error) {
	var o T
	// Create a pointer to T and assert it implements client.Object
	ptr := any(&o).(client.Object)
	err := cl.Get(ctx, key, ptr, opts...)
	if err != nil {
		var zero T
		return zero, err
	}
	return o, nil
}

func List[T any](ctx context.Context, cl client.Client, opts ...client.ListOption) ([]T, error) {
	var list client.ObjectList
	// Map item type to its corresponding list type
	switch any((*T)(nil)).(type) {
	case *v1alpha3.ReplicatedVolumeReplica:
		list = &v1alpha3.ReplicatedVolumeReplicaList{}
	case *v1alpha3.ReplicatedVolume:
		list = &v1alpha3.ReplicatedVolumeList{}
	default:
		return nil, fmt.Errorf("unsupported item type %T - list type not registered", (*T)(nil))
	}

	err := cl.List(ctx, list, opts...)
	if err != nil {
		return nil, err
	}

	// Extract Items field using reflection
	listValue := reflect.ValueOf(list).Elem()
	itemsField := listValue.FieldByName("Items")
	return itemsField.Interface().([]T), nil
}

var _ = Describe("Reconciler", func() {
	var cl client.Client
	var rec *rvr_status_config_peers.Reconciler
	var scheme = newScheme()

	BeforeEach(func() {
		cl = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(
				&v1alpha3.ReplicatedVolumeReplica{},
				&v1alpha3.ReplicatedVolume{}).
			Build()
		rec = rvr_status_config_peers.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: "not-existing-rv",
			},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	When("ReplicatedVolume created", func() {
		var rv *v1alpha3.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rv",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "test-storage-class",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed())
			// Get RV to retrieve its UID
			Expect(cl.Get(ctx, client.ObjectKey{Name: "test-rv"}, rv)).To(Succeed())
		})

		expectReconcileSuccessfully := func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: rv.Name,
				},
			})).To(Equal(reconcile.Result{}))
		}

		// Convenience wrapper for RVRs with owner reference setOwnerReference
		setOwnerReference := func(rvr *v1alpha3.ReplicatedVolumeReplica) {
			if rv != nil {
				Expect(controllerutil.SetControllerReference(rv, rvr, scheme)).To(Succeed())
			}
		}
		whenRVRCreated := func(desc string, Resources []v1alpha3.ReplicatedVolumeReplica, fn func(vars *whenResourcesCreatedVars[v1alpha3.ReplicatedVolumeReplica]), args ...any) {
			whenResourcesCreated(desc, &cl, Resources, fn, setOwnerReference, args...)
		}

		makeReady := func(rvr *v1alpha3.ReplicatedVolumeReplica, nodeName string, nodeId uint, address v1alpha3.Address) {
			if rvr.Status == nil {
				rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
			}

			if rvr.Status.Config == nil {
				rvr.Status.Config = &v1alpha3.DRBDConfig{}
			}

			rvr.Status.Config.NodeId = &nodeId
			rvr.Status.Config.Address = &address
		}

		whenRVRCreated("first replica created", []v1alpha3.ReplicatedVolumeReplica{{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
			Status:     &v1alpha3.ReplicatedVolumeReplicaStatus{Config: &v1alpha3.DRBDConfig{}},
		},
		}, func(firstCreated *whenResourcesCreatedVars[v1alpha3.ReplicatedVolumeReplica]) {
			It("should not have peers", func(ctx SpecContext) {
				expectReconcileSuccessfully(ctx)
				Expect(Get[v1alpha3.ReplicatedVolumeReplica](ctx, cl, client.ObjectKey{Name: "rvr-1"})).
					To(HaveNoPeers())
			})

			Context("if rvr-1 created is ready", func() {
				BeforeEach(func() {
					makeReady(&firstCreated.Resources[0], "node-1", 1, v1alpha3.Address{IPv4: "192.168.1.1", Port: 7000})
				})

				It("should skip RVR not owned by the ReplicatedVolume", func(ctx SpecContext) {
					// Create RVR not controlled by test-rv
					rvr2 := &v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-2",
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion: "storage.deckhouse.io/v1alpha3",
									Kind:       "ReplicatedVolume",
									Name:       "other-rv", // Different RV
									UID:        types.UID("test-uid-other-rv"),
									Controller: func() *bool { b := true; return &b }(),
								},
							},
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "other-rv",
							NodeName:             "node-2",
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
							Config: &v1alpha3.DRBDConfig{
								NodeId:  func() *uint { u := uint(2); return &u }(),
								Address: &v1alpha3.Address{IPv4: "192.168.1.2", Port: 7000},
							},
						},
					}
					Expect(cl.Create(ctx, rvr2)).To(Succeed())

					expectReconcileSuccessfully(ctx)

					// rvr1 should have no peers (rvr2 is not controlled by test-rv)
					Expect(Get[v1alpha3.ReplicatedVolumeReplica](ctx, cl, client.ObjectKey{Name: "rvr-1"})).To(HaveNoPeers())
				})

				It("should skip RVR without nodeName", func(ctx SpecContext) {

					// Create RVR without nodeName
					rvr2 := &v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "rvr-2",
							OwnerReferences: []metav1.OwnerReference{ownerRef(scheme, rv)},
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "test-rv",
							NodeName:             "", // Empty nodeName
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
							Config: &v1alpha3.DRBDConfig{
								NodeId:  func() *uint { u := uint(2); return &u }(),
								Address: &v1alpha3.Address{IPv4: "192.168.1.2", Port: 7000},
							},
						},
					}
					Expect(cl.Create(ctx, rvr2)).To(Succeed())

					expectReconcileSuccessfully(ctx)

					// rvr1 should have no peers (rvr2 is not controlled by test-rv)
					Expect(Get[v1alpha3.ReplicatedVolumeReplica](ctx, cl, client.ObjectKey{Name: "rvr-1"})).To(HaveNoPeers())
				})

				It("should skip RVR without nodeId", func(ctx SpecContext) {
					// Create RVR without nodeId
					rvr2 := &v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "rvr-2",
							OwnerReferences: []metav1.OwnerReference{ownerRef(scheme, rv)},
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "test-rv",
							NodeName:             "node-2",
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
							Config: &v1alpha3.DRBDConfig{
								// No NodeId
								Address: &v1alpha3.Address{IPv4: "192.168.1.2", Port: 7000},
							},
						},
					}
					Expect(cl.Create(ctx, rvr2)).To(Succeed())

					expectReconcileSuccessfully(ctx)

					Expect(Get[v1alpha3.ReplicatedVolumeReplica](ctx, cl, client.ObjectKey{Name: "rvr-1"})).To(HaveNoPeers())
				})

				It("should skip RVR without address", func(ctx SpecContext) {
					// Create RVR without address
					rvr2 := &v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "rvr-2",
							OwnerReferences: []metav1.OwnerReference{ownerRef(scheme, rv)},
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "test-rv",
							NodeName:             "node-2",
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
							Config: &v1alpha3.DRBDConfig{
								NodeId: func() *uint { u := uint(2); return &u }(),
								// No Address
							},
						},
					}
					Expect(cl.Create(ctx, rvr2)).To(Succeed())

					expectReconcileSuccessfully(ctx)

					Expect(Get[v1alpha3.ReplicatedVolumeReplica](ctx, cl, client.ObjectKey{Name: "rvr-1"})).To(HaveNoPeers())
				})

				It("should have no peers", func(ctx SpecContext) {
					expectReconcileSuccessfully(ctx)
					Expect(Get[v1alpha3.ReplicatedVolumeReplica](ctx, cl, client.ObjectKey{Name: "rvr-1"})).
						To(HaveNoPeers())
				})

				whenRVRCreated("second not ready replica created", []v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "test-rv",
							NodeName:             "node-2"},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{Config: &v1alpha3.DRBDConfig{}},
					},
				}, func(created2 *whenResourcesCreatedVars[v1alpha3.ReplicatedVolumeReplica]) {
					It("rvr-1 should have no peers", func(ctx SpecContext) {
						Expect(Get[v1alpha3.ReplicatedVolumeReplica](ctx, cl, client.ObjectKey{Name: "rvr-1"})).
							To(HaveNoPeers())
					})

					It("rvr-2 should have no peers", func(ctx SpecContext) {
						Expect(Get[v1alpha3.ReplicatedVolumeReplica](ctx, cl, client.ObjectKey{Name: "rvr-2"})).
							To(HaveNoPeers())
					})

					Context("if rvr-2 created ready", func() {
						BeforeEach(func() {
							makeReady(&created2.Resources[0], "node-2", 2, v1alpha3.Address{IPv4: "192.168.1.4", Port: 7001})
						})
						It("should update peers when RVR transitions to ready state", func(ctx SpecContext) {
							expectReconcileSuccessfully(ctx)
							Expect(List[v1alpha3.ReplicatedVolumeReplica](ctx, cl)).To(SatisfyAll(
								HaveAllPeersSetForAll(),
								HaveLen(2)))
						})
					})
				})

				whenRVRCreated("second ready replica created", []v1alpha3.ReplicatedVolumeReplica{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-2",
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							NodeName: "node-2",
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
							Config: &v1alpha3.DRBDConfig{
								NodeId:  func() *uint { ret := uint(1); return &ret }(),
								Address: &v1alpha3.Address{IPv4: "192.168.1.2", Port: 7000},
							},
						},
					},
				}, func(vars2 *whenResourcesCreatedVars[v1alpha3.ReplicatedVolumeReplica]) {
					It("should update peers on existing ready RVRs", func(ctx SpecContext) {
						expectReconcileSuccessfully(ctx)

						Expect(List[v1alpha3.ReplicatedVolumeReplica](ctx, cl)).To(SatisfyAll(
							HaveAllPeersSetForAll(),
							HaveLen(2)))
					})
				})
			})
		})

		whenRVRCreated("three replicas created", []v1alpha3.ReplicatedVolumeReplica{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
				Status:     &v1alpha3.ReplicatedVolumeReplicaStatus{Config: &v1alpha3.DRBDConfig{}},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
				Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
				Status:     &v1alpha3.ReplicatedVolumeReplicaStatus{Config: &v1alpha3.DRBDConfig{}},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-3"},
				Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-3"},
				Status:     &v1alpha3.ReplicatedVolumeReplicaStatus{Config: &v1alpha3.DRBDConfig{}},
			},
		}, func(created *whenResourcesCreatedVars[v1alpha3.ReplicatedVolumeReplica]) {
			Context("if only first replica was ready", func() {
				BeforeEach(func() {
					for i := range 1 {
						nodeId := uint(i)
						address := v1alpha3.Address{IPv4: fmt.Sprintf("192.168.1.%d", i+1), Port: 7000 + uint(i)}
						Expect(created.Resources[i].Status).ToNot(BeNil())
						Expect(created.Resources[i].Status.Config).ToNot(BeNil())
						created.Resources[i].Status.Config.NodeId = &nodeId
						created.Resources[i].Status.Config.Address = &address
					}
				})

				It("should not have any peers", func(ctx SpecContext) {
					expectReconcileSuccessfully(ctx)
					Expect(List[v1alpha3.ReplicatedVolumeReplica](ctx, cl)).To(HaveEach(HaveNoPeers()))
				})

				When("all the rest becomes ready", func() {
					JustBeforeEach(func(ctx SpecContext) {
						for i, rvr := range created.Resources[1:] {
							By(fmt.Sprintf("Making ready %s", rvr.Name))

							Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvr), &rvr)).To(Succeed())
							nodeId := uint(i)
							address := v1alpha3.Address{IPv4: fmt.Sprintf("192.168.1.%d", i+1), Port: 7000 + uint(i)}
							rvr.Status.Config.NodeId = &nodeId
							rvr.Status.Config.Address = &address
							Expect(cl.Status().Update(ctx, &rvr)).To(Succeed())
						}

						for _, rvr := range created.Resources {
							By(fmt.Sprintf("Checking %s", rvr.Name))
							Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvr), &rvr)).To(Succeed())
							Expect(rvr).To(SatisfyAll(
								HaveField("Spec.NodeName", Not(BeEmpty())),
								HaveField("Status.Config.NodeId", Not(BeNil())),
								HaveField("Status.Config.Address", Not(BeNil())),
							))
						}
					})

					It("should have all peers set", func(ctx SpecContext) {
						expectReconcileSuccessfully(ctx)
						Expect(List[v1alpha3.ReplicatedVolumeReplica](ctx, cl)).To(HaveAllPeersSetForAll())
					})
				})
			})

			Context("if all replicas were ready", func() {
				BeforeEach(func() {
					for i := range created.Resources {
						nodeId := uint(i)
						created.Resources[i].Status.Config.NodeId = &nodeId
						address := v1alpha3.Address{IPv4: fmt.Sprintf("192.168.1.%d", i+1), Port: 7000 + uint(i)}
						created.Resources[i].Status.Config.Address = &address
					}
				})

				It("should gave all peers set", func(ctx SpecContext) {
					expectReconcileSuccessfully(ctx)
					Expect(List[v1alpha3.ReplicatedVolumeReplica](ctx, cl)).To(HaveAllPeersSetForAll())
				})

				When("second rvr deleted", func() {
					JustBeforeEach(func(ctx SpecContext) {
						// Delete rvr2
						Expect(cl.Delete(ctx, &created.Resources[1])).To(Succeed())
					})

					It("should remove deleted RVR from peers of remaining RVRs", func(ctx SpecContext) {
						expectReconcileSuccessfully(ctx)
						// Verify rvr1 and rvr3 no longer have node-2 in peers
						updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
						Expect(updatedRVR1.Status.Config.Peers).To(And(
							Not(HaveKey("node-2")),
							HaveKey("node-3"),
						))

						updatedRVR3 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updatedRVR3)).To(Succeed())
						Expect(updatedRVR3.Status.Config.Peers).To(And(
							Not(HaveKey("node-2")),
							HaveKey("node-1"),
						))
					})
				})

				When("multiple RVRs exist on same node", func() {
					BeforeEach(func() {
						// Use all 3 RVRs, but set node-2 to node-1 for rvr-2
						created.Resources[1].Spec.NodeName = "node-1" // Same node as rvr-1
						nodeId1 := uint(1)
						nodeId2 := uint(2)
						nodeId3 := uint(3)
						address1 := v1alpha3.Address{IPv4: "192.168.1.1", Port: 7000}
						address2 := v1alpha3.Address{IPv4: "192.168.1.1", Port: 7001} // Same IP, different port
						address3 := v1alpha3.Address{IPv4: "192.168.1.2", Port: 7000}
						created.Resources[0].Status.Config.NodeId = &nodeId1
						created.Resources[0].Status.Config.Address = &address1
						created.Resources[1].Status.Config.NodeId = &nodeId2
						created.Resources[1].Status.Config.Address = &address2
						created.Resources[2].Status.Config.NodeId = &nodeId3
						created.Resources[2].Status.Config.Address = &address3
					})

					It("should only keep one peer entry per node", func(ctx SpecContext) {
						_, err := rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})
						Expect(err).NotTo(HaveOccurred())

						// rvr3 should only have one peer entry for node-1 (the first one found)
						updatedRVR3 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updatedRVR3)).To(Succeed())
						Expect(updatedRVR3.Status.Config.Peers).To(And(
							HaveKey("node-1"),
							HaveLen(1),
						))
					})
				})

				When("peers are already correct", func() {
					BeforeEach(func() {
						// Use only first 2 RVRs
						created.Resources = created.Resources[:2]
						nodeId1 := uint(1)
						nodeId2 := uint(2)
						address1 := v1alpha3.Address{IPv4: "192.168.1.1", Port: 7000}
						address2 := v1alpha3.Address{IPv4: "192.168.1.2", Port: 7000}
						created.Resources[0].Status.Config.NodeId = &nodeId1
						created.Resources[0].Status.Config.Address = &address1
						created.Resources[1].Status.Config.NodeId = &nodeId2
						created.Resources[1].Status.Config.Address = &address2
					})

					It("should not update if peers are unchanged", func(ctx SpecContext) {

						// First reconcile
						_, err := rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})
						Expect(err).NotTo(HaveOccurred())

						// Get the state after first reconcile
						updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
						initialPeers := updatedRVR1.Status.Config.Peers

						// Second reconcile - should not change
						_, err = rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})
						Expect(err).NotTo(HaveOccurred())

						// Verify peers are unchanged
						updatedRVR1After := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR1After)).To(Succeed())
						Expect(updatedRVR1After.Status.Config.Peers).To(Equal(initialPeers))
					})
				})

				Context("with diskless RVRs", func() {
					BeforeEach(func() {
						// Use only first 2 RVRs, set second one as diskless
						created.Resources = created.Resources[:2]
						created.Resources[1].Spec.Diskless = true
						nodeId1 := uint(1)
						nodeId2 := uint(2)
						address1 := v1alpha3.Address{IPv4: "192.168.1.1", Port: 7000}
						address2 := v1alpha3.Address{IPv4: "192.168.1.2", Port: 7000}
						created.Resources[0].Status.Config.NodeId = &nodeId1
						created.Resources[0].Status.Config.Address = &address1
						created.Resources[1].Status.Config.NodeId = &nodeId2
						created.Resources[1].Status.Config.Address = &address2
					})

					It("should include diskless flag in peer information", func(ctx SpecContext) {
						_, err := rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})
						Expect(err).NotTo(HaveOccurred())

						// Verify rvr1 has rvr2 with diskless flag
						updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
						Expect(updatedRVR1.Status.Config.Peers).To(HaveKeyWithValue("node-2", HaveField("Diskless", BeTrue())))
					})
				})
			})
		})
	})
})
