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

package hooks_common

import (
	"context"
	"errors"
	"fmt"

	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	sv1 "k8s.io/api/storage/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
	"github.com/deckhouse/module-sdk/pkg"
	"github.com/deckhouse/module-sdk/pkg/registry"
	"github.com/deckhouse/sds-common-lib/kubeclient"
)

const hookName = "remove-finalizers-on-module-delete"

func removeFinalizersFromObject(ctx context.Context, cl client.Client, obj client.Object, logger pkg.Logger) error {
	logger.Info(fmt.Sprintf("[%s]: Removing finalizers from %s %s", hookName, obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName()))

	patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	obj.SetFinalizers(nil)

	if err := cl.Patch(ctx, obj, patch); err != nil {
		return fmt.Errorf("failed to patch %s %s: %w", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
	}
	return nil
}

var _ = registry.RegisterFunc(configRemoveFinalizersOnModuleDelete, handlerRemoveFinalizersOnModuleDelete)

var configRemoveFinalizersOnModuleDelete = &pkg.HookConfig{
	OnAfterDeleteHelm: &pkg.OrderedConfig{Order: 10},
}

func handlerRemoveFinalizersOnModuleDelete(ctx context.Context, input *pkg.HookInput) error {
	input.Logger.Info(fmt.Sprintf("[%s]: Started removing finalizers on module delete", hookName))
	var resultErr error

	cl, err := kubeclient.New(
		admissionregistrationv1.AddToScheme,
		clientgoscheme.AddToScheme,
		extv1.AddToScheme,
		v1.AddToScheme,
		sv1.AddToScheme,
		snapv1.AddToScheme)
	if err != nil {
		return fmt.Errorf("failed to initialize kube client: %w", err)
	}

	// Remove finalizers from Secrets in module namespace
	secretList := &corev1.SecretList{}
	if err := cl.List(ctx, secretList, client.InNamespace(consts.ModuleNamespace)); err != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("[%s]: failed to list secrets: %w", hookName, err))
	} else {
		for i := range secretList.Items {
			if err := removeFinalizersFromObject(ctx, cl, &secretList.Items[i], input.Logger); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}

	// Remove finalizers from ConfigMaps in module namespace
	configMapList := &corev1.ConfigMapList{}
	if err := cl.List(ctx, configMapList, client.InNamespace(consts.ModuleNamespace)); err != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("[%s]: failed to list configmaps: %w", hookName, err))
	} else {
		for i := range configMapList.Items {
			if err := removeFinalizersFromObject(ctx, cl, &configMapList.Items[i], input.Logger); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}

	// Delete ValidatingWebhookConfigurations if configured
	for _, name := range consts.WebhookConfigurationsToDelete {
		vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		if err := cl.Get(ctx, client.ObjectKey{Name: name}, vwc); err == nil {
			input.Logger.Info(fmt.Sprintf("[%s]: Deleting ValidatingWebhookConfiguration %s", hookName, name))
			if err := cl.Delete(ctx, vwc); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("[%s]: failed to delete ValidatingWebhookConfiguration %s: %w", hookName, name, err))
			}
		}
	}

	// Remove finalizers from StorageClass if controller creates them
	for _, provisioner := range consts.AllowedProvisioners {
		scList := &storagev1.StorageClassList{}
		if err := cl.List(ctx, scList); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("[%s]: failed to list storage classes: %w", hookName, err))
			break
		}
		for i := range scList.Items {
			if scList.Items[i].Provisioner != provisioner {
				continue
			}
			if err := removeFinalizersFromObject(ctx, cl, &scList.Items[i], input.Logger); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}

	// Remove finalizers from CRs from module's crd folder
	for _, crgvk := range consts.CRGVKsForFinalizerRemoval {
		ns := ""
		if crgvk.Namespaced {
			ns = consts.ModuleNamespace
		}
		crList := &unstructured.UnstructuredList{}
		crList.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   crgvk.Group,
			Version: crgvk.Version,
			Kind:    crgvk.Kind + "List",
		})
		if err := cl.List(ctx, crList, client.InNamespace(ns)); err != nil {
			input.Logger.Info(fmt.Sprintf("[%s]: skipping CR %s (may not exist): %v", hookName, crgvk.Kind, err))
			continue
		}
		for i := range crList.Items {
			cr := &crList.Items[i]
			if len(cr.GetFinalizers()) == 0 {
				continue
			}
			if err := removeFinalizersFromObject(ctx, cl, cr, input.Logger); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}

	input.Logger.Info(fmt.Sprintf("[%s]: Finished removing finalizers on module delete", hookName))
	return resultErr
}
