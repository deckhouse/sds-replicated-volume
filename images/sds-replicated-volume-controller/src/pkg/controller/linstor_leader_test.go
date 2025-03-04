package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	v12 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sds-replicated-volume-controller/pkg/logger"
)

func TestLinstorLeaderController(t *testing.T) {
	var (
		cl                = newFakeClient()
		ctx               = context.Background()
		log               = logger.Logger{}
		namespace         = "test-ns"
		leaseName         = "test-lease"
		linstorLabelValue = "test"
	)

	t.Run("no_lease_returns_error", func(t *testing.T) {
		err := reconcileLinstorControllerPods(ctx, cl, log, namespace, leaseName)
		assert.Error(t, err)
	})

	t.Run("app_label_not_exists_linstor_label_exists_does_nothing", func(t *testing.T) {
		const (
			podName = "first-pod"
		)
		podList := &v1.PodList{
			Items: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName,
						Namespace: namespace,
						Labels: map[string]string{
							LinstorLeaderLabel: linstorLabelValue,
						},
					},
				},
			},
		}

		lease := &v12.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: namespace,
			},
			Spec: v12.LeaseSpec{
				HolderIdentity: nil,
			},
		}

		var err error
		for _, pod := range podList.Items {
			err = cl.Create(ctx, &pod)
			if err != nil {
				t.Error(err)
			}
		}
		err = cl.Create(ctx, lease)
		if err != nil {
			t.Error(err)
		}

		if assert.NoError(t, err) {
			defer func() {
				for _, pod := range podList.Items {
					err = cl.Delete(ctx, &pod)
					if err != nil {
						fmt.Println(fmt.Errorf("unexpected ERROR: %w", err))
					}
				}

				err = cl.Delete(ctx, lease)
				if err != nil {
					fmt.Println(fmt.Errorf("unexpected ERROR: %w", err))
				}
			}()
		}

		podWithLabel := &v1.Pod{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      podName,
			Namespace: namespace,
		}, podWithLabel)

		if assert.NoError(t, err) {
			assert.Equal(t, podWithLabel.Labels[LinstorLeaderLabel], linstorLabelValue)
		}

		err = reconcileLinstorControllerPods(ctx, cl, log, namespace, leaseName)
		assert.NoError(t, err)

		podWithLabelAfretReconcile := &v1.Pod{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      podName,
			Namespace: namespace,
		}, podWithLabelAfretReconcile)

		if assert.NoError(t, err) {
			_, exist := podWithLabelAfretReconcile.Labels[LinstorLeaderLabel]
			assert.True(t, exist)
		}
	})

	t.Run("linstor_label_exists_lease_HolderIdentity_is_nil_removes_label", func(t *testing.T) {
		const (
			podName = "first-pod"
		)
		podList := &v1.PodList{
			Items: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName,
						Namespace: namespace,
						Labels: map[string]string{
							"app":              LinstorControllerAppLabelValue,
							LinstorLeaderLabel: linstorLabelValue,
						},
					},
				},
			},
		}

		lease := &v12.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: namespace,
			},
			Spec: v12.LeaseSpec{
				HolderIdentity: nil,
			},
		}

		var err error
		for _, pod := range podList.Items {
			err = cl.Create(ctx, &pod)
			if err != nil {
				t.Error(err)
			}
		}
		err = cl.Create(ctx, lease)
		if err != nil {
			t.Error(err)
		}

		if assert.NoError(t, err) {
			defer func() {
				for _, pod := range podList.Items {
					err = cl.Delete(ctx, &pod)
					if err != nil {
						fmt.Println(fmt.Errorf("unexpected ERROR: %w", err))
					}
				}

				err = cl.Delete(ctx, lease)
				if err != nil {
					fmt.Println(fmt.Errorf("unexpected ERROR: %w", err))
				}
			}()
		}

		podWithLabel := &v1.Pod{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      podName,
			Namespace: namespace,
		}, podWithLabel)

		if assert.NoError(t, err) {
			assert.Equal(t, podWithLabel.Labels[LinstorLeaderLabel], linstorLabelValue)
		}

		err = reconcileLinstorControllerPods(ctx, cl, log, namespace, leaseName)
		assert.NoError(t, err)

		podWithoutLabel := &v1.Pod{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      podName,
			Namespace: namespace,
		}, podWithoutLabel)

		if assert.NoError(t, err) {
			_, exist := podWithoutLabel.Labels[LinstorLeaderLabel]
			assert.False(t, exist)
		}
	})

	t.Run("linstor_label_exists_lease_HolderIdentity_not_nil_pod_name_not_equals_HolderIdentity_removes_label", func(t *testing.T) {
		const (
			podName = "first-pod"
		)
		podList := &v1.PodList{
			Items: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName,
						Namespace: namespace,
						Labels: map[string]string{
							"app":              LinstorControllerAppLabelValue,
							LinstorLeaderLabel: linstorLabelValue,
						},
					},
				},
			},
		}

		hi := "another-name"
		lease := &v12.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: namespace,
			},
			Spec: v12.LeaseSpec{
				HolderIdentity: &hi,
			},
		}

		var err error
		for _, pod := range podList.Items {
			err = cl.Create(ctx, &pod)
			if err != nil {
				t.Error(err)
			}
		}
		err = cl.Create(ctx, lease)

		if assert.NoError(t, err) {
			defer func() {
				for _, pod := range podList.Items {
					err = cl.Delete(ctx, &pod)
					if err != nil {
						fmt.Println(fmt.Errorf("unexpected ERROR: %w", err))
					}
				}

				err = cl.Delete(ctx, lease)
				if err != nil {
					fmt.Println(fmt.Errorf("unexpected ERROR: %w", err))
				}
			}()
		}

		podWithLabel := &v1.Pod{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      podName,
			Namespace: namespace,
		}, podWithLabel)

		if assert.NoError(t, err) {
			assert.Equal(t, podWithLabel.Labels[LinstorLeaderLabel], linstorLabelValue)
		}

		err = reconcileLinstorControllerPods(ctx, cl, log, namespace, leaseName)
		assert.NoError(t, err)

		podWithoutLabel := &v1.Pod{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      podName,
			Namespace: namespace,
		}, podWithoutLabel)

		if assert.NoError(t, err) {
			_, exist := podWithoutLabel.Labels[LinstorLeaderLabel]
			assert.False(t, exist)
		}
	})

	t.Run("linstor_label_not_exists_lease_HolderIdentity_not_nil_pod_name_equals_HolderIdentity_set_label_true", func(t *testing.T) {
		const (
			podName = "first-pod"
		)
		podList := &v1.PodList{
			Items: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      podName,
						Namespace: namespace,
						Labels: map[string]string{
							"app": LinstorControllerAppLabelValue,
						},
					},
				},
			},
		}

		hi := podName
		lease := &v12.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: namespace,
			},
			Spec: v12.LeaseSpec{
				HolderIdentity: &hi,
			},
		}

		var err error
		for _, pod := range podList.Items {
			err = cl.Create(ctx, &pod)
			if err != nil {
				t.Error(err)
			}
		}
		err = cl.Create(ctx, lease)

		if assert.NoError(t, err) {
			defer func() {
				for _, pod := range podList.Items {
					err = cl.Delete(ctx, &pod)
					if err != nil {
						fmt.Println(fmt.Errorf("unexpected ERROR: %w", err))
					}
				}

				err = cl.Delete(ctx, lease)
				if err != nil {
					fmt.Println(fmt.Errorf("unexpected ERROR: %w", err))
				}
			}()
		}

		podWithoutLabel := &v1.Pod{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      podName,
			Namespace: namespace,
		}, podWithoutLabel)

		if assert.NoError(t, err) {
			_, exist := podWithoutLabel.Labels[LinstorLeaderLabel]
			assert.False(t, exist)
		}

		err = reconcileLinstorControllerPods(ctx, cl, log, namespace, leaseName)
		assert.NoError(t, err)

		podWithLabel := &v1.Pod{}
		err = cl.Get(ctx, client.ObjectKey{
			Name:      podName,
			Namespace: namespace,
		}, podWithLabel)

		if assert.NoError(t, err) {
			assert.Equal(t, podWithLabel.Labels[LinstorLeaderLabel], "true")
		}
	})
}
