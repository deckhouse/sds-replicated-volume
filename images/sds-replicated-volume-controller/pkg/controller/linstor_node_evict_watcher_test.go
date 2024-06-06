package controller

import (
	"context"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/strings/slices"
	"testing"
)

func TestLinstorNodeEvictWatcher(t *testing.T) {
	ctx := context.Background()
	cl := newFakeClient()

	t.Run("filterOutLinstorCRDs", func(t *testing.T) {
		crds := []v1.CustomResourceDefinition{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "resourcedefinitions.internal.linstor.linbit.com.yaml",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nodestorpool.internal.linstor.linbit.com.yaml",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some.custom.crd",
				},
			},
		}

		var err error
		for _, crd := range crds {
			err = cl.Create(ctx, &crd)
			if err != nil {
				t.Error(err)
			}
		}

		crdList := &v1.CustomResourceDefinitionList{}
		err = cl.List(ctx, crdList)
		if err != nil {
			t.Error(err)
		}

		linstorCRDs, err := filterOutLinstorCRDs(crdList)
		if err != nil {
			t.Error(err)
		}

		if assert.Equal(t, 2, len(linstorCRDs)) {
			expectedNames := []string{"resourcedefinitions.internal.linstor.linbit.com.yaml", "nodestorpool.internal.linstor.linbit.com.yaml"}

			for _, crd := range linstorCRDs {
				assert.True(t, slices.Contains(expectedNames, crd.Name))
			}
		}
	})
}
