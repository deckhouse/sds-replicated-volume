package controller

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	lapi "github.com/LINBIT/golinstor/client"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"sds-replicated-volume-controller/pkg/logger"
)

func TestLinstorPortRangeWatcher(t *testing.T) {
	ctx := context.Background()
	log := logger.Logger{}
	cl := newFakeClient()

	t.Run("updateConfigMapLabel", func(t *testing.T) {
		const (
			name  = "test"
			value = "my-value"
		)

		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}

		err := cl.Create(ctx, cm)
		if err != nil {
			t.Error(err)
		}

		err = updateConfigMapLabel(ctx, cl, cm, value)
		if assert.NoError(t, err) {
			updatedCm := &v1.ConfigMap{}
			err = cl.Get(ctx, client.ObjectKey{
				Name: name,
			}, updatedCm)
			if err != nil {
				t.Error(err)
			}

			v, ok := updatedCm.Labels[incorrectPortRangeKey]
			if assert.True(t, ok) {
				assert.Equal(t, value, v)
			}
		}
	})

	t.Run("ReconcileConfigMapEvent_if_maxPort_less_minPort_returns_false_err", func(t *testing.T) {
		const (
			name = "test1"

			minValue = "2000"
			maxValue = "1999"
		)

		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Data: map[string]string{
				minPortKey: minValue,
				maxPortKey: maxValue,
			},
		}

		err := cl.Create(ctx, cm)
		if err != nil {
			t.Error(err)
		}

		req := reconcile.Request{}
		req.NamespacedName = types.NamespacedName{
			Name: name,
		}

		shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, &lapi.Client{
			Controller: &lapi.ControllerService{},
		}, req, log)

		if assert.ErrorContains(t, err, fmt.Sprintf("range start port %s is less than range end port %s", minValue, maxValue)) {
			assert.False(t, shouldRequeue)

			updatedCm := &v1.ConfigMap{}
			err = cl.Get(ctx, client.ObjectKey{
				Name: name,
			}, updatedCm)
			if err != nil {
				t.Error(err)
			}

			v, ok := updatedCm.Labels[incorrectPortRangeKey]
			if assert.True(t, ok) {
				assert.Equal(t, "true", v)
			}
		}
	})

	t.Run("ReconcileConfigMapEvent_if_maxPort_more_than_max_value_returns_false_err", func(t *testing.T) {
		const (
			name = "test2"

			minValueInt = minPortValue
			maxValueInt = maxPortValue + 1
		)

		maxValue := strconv.Itoa(maxValueInt)
		minValue := strconv.Itoa(minValueInt)

		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Data: map[string]string{
				minPortKey: minValue,
				maxPortKey: maxValue,
			},
		}

		err := cl.Create(ctx, cm)
		if err != nil {
			t.Error(err)
		}

		req := reconcile.Request{}
		req.NamespacedName = types.NamespacedName{
			Name: name,
		}

		shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, &lapi.Client{
			Controller: &lapi.ControllerService{},
		}, req, log)

		if assert.ErrorContains(t, err, fmt.Sprintf("range end port %d must be less then %d", maxValueInt, maxPortValue)) {
			assert.False(t, shouldRequeue)

			updatedCm := &v1.ConfigMap{}
			err = cl.Get(ctx, client.ObjectKey{
				Name: name,
			}, updatedCm)
			if err != nil {
				t.Error(err)
			}

			v, ok := updatedCm.Labels[incorrectPortRangeKey]
			if assert.True(t, ok) {
				assert.Equal(t, "true", v)
			}
		}
	})

	t.Run("ReconcileConfigMapEvent_if_minPort_less_than_min_value_returns_false_err", func(t *testing.T) {
		const (
			name = "test3"

			minValueInt = minPortValue - 1
			maxValueInt = maxPortValue
		)

		maxValue := strconv.Itoa(maxValueInt)
		minValue := strconv.Itoa(minValueInt)

		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Data: map[string]string{
				minPortKey: minValue,
				maxPortKey: maxValue,
			},
		}

		err := cl.Create(ctx, cm)
		if err != nil {
			t.Error(err)
		}

		req := reconcile.Request{}
		req.NamespacedName = types.NamespacedName{
			Name: name,
		}

		shouldRequeue, err := ReconcileConfigMapEvent(ctx, cl, &lapi.Client{
			Controller: &lapi.ControllerService{},
		}, req, log)

		if assert.ErrorContains(t, err, fmt.Sprintf("range start port %d must be more then %d", minValueInt, minPortValue)) {
			assert.False(t, shouldRequeue)

			updatedCm := &v1.ConfigMap{}
			err = cl.Get(ctx, client.ObjectKey{
				Name: name,
			}, updatedCm)
			if err != nil {
				t.Error(err)
			}

			v, ok := updatedCm.Labels[incorrectPortRangeKey]
			if assert.True(t, ok) {
				assert.Equal(t, "true", v)
			}
		}
	})
}
