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

package handlers

import (
	"context"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	kwhhttp "github.com/slok/kubewebhook/v2/pkg/http"
	"github.com/slok/kubewebhook/v2/pkg/log"
	"github.com/slok/kubewebhook/v2/pkg/model"
	kwhvalidating "github.com/slok/kubewebhook/v2/pkg/webhook/validating"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/resource/v1alpha3"
	sv1 "k8s.io/api/storage/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	d8commonapi "github.com/deckhouse/sds-common-lib/api/v1alpha1"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

func NewKubeClient(kubeconfigPath string) (client.Client, error) {
	var config *rest.Config
	var err error

	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("kubeconfig")
	}

	controllerruntime.SetLogger(logr.New(ctrllog.NullLogSink{}))

	config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)

	if err != nil {
		return nil, err
	}

	var (
		resourcesSchemeFuncs = []func(*apiruntime.Scheme) error{
			v1alpha3.AddToScheme,
			d8commonapi.AddToScheme,
			snc.AddToScheme,
			clientgoscheme.AddToScheme,
			extv1.AddToScheme,
			v1.AddToScheme,
			sv1.AddToScheme,
		}
	)

	scheme := apiruntime.NewScheme()
	for _, f := range resourcesSchemeFuncs {
		err = f(scheme)
		if err != nil {
			return nil, err
		}
	}

	clientOpts := client.Options{
		Scheme: scheme,
	}

	return client.New(config, clientOpts)
}

func GetValidatingWebhookHandler(validationFunc func(ctx context.Context, _ *model.AdmissionReview, obj metav1.Object) (*kwhvalidating.ValidatorResult, error), validatorID string, obj metav1.Object, logger log.Logger) (http.Handler, error) {
	validatorFunc := kwhvalidating.ValidatorFunc(validationFunc)

	validatingWebhookConfig := kwhvalidating.WebhookConfig{
		ID:        validatorID,
		Obj:       obj,
		Validator: validatorFunc,
		Logger:    logger,
	}

	mutationWebhook, err := kwhvalidating.NewWebhook(validatingWebhookConfig)
	if err != nil {
		return nil, err
	}

	mutationWebhookHandler, err := kwhhttp.HandlerFor(kwhhttp.HandlerConfig{Webhook: mutationWebhook, Logger: logger})

	return mutationWebhookHandler, err
}
