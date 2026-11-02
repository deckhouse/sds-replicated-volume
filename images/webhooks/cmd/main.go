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

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"

	"github.com/deckhouse/sds-common-lib/logger"
	"github.com/deckhouse/sds-common-lib/logger/kwhlogger"
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/webhooks/handlers"
)

type config struct {
	certFile string
	keyFile  string
}

//goland:noinspection SpellCheckingInspection
func httpHandlerHealthz(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(w, "Ok.")
}

func initFlags() config {
	cfg := config{}

	fl := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fl.StringVar(&cfg.certFile, "tls-cert-file", "", "TLS certificate file")
	fl.StringVar(&cfg.keyFile, "tls-key-file", "", "TLS key file")

	err := fl.Parse(os.Args[1:])
	if err != nil {
		fmt.Printf("error parsing flags, err: %s\n", err.Error())
		os.Exit(1)
	}
	return cfg
}

const (
	port           = ":8443"
	RSCValidatorID = "RSCValidator"
	RSPValidatorID = "RSPValidator"
	SCValidatorID  = "SCValidator"
	PVCValidatorID = "PVCValidator"
	LVGValidatorID = "LVGValidator"
)

func main() {
	// LOG_LEVEL, the numeric 0..4 value lib-helm emits. This webhook used to ignore
	// the module setting and log at Debug whatever it said.
	log, err := logger.NewLoggerFromEnv(logger.InfoLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to create the logger: %s\n", err)
		os.Exit(1)
	}
	kwhLog := kwhlogger.New(log)
	handlers.SetLogger(log)

	cfg := initFlags()

	rspValidatingWebhookHandler, err := handlers.GetValidatingWebhookHandler(handlers.RSPValidate, RSPValidatorID, &srv.ReplicatedStoragePool{}, kwhLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating rspValidatingWebhookHandler: %s", err)
		os.Exit(1)
	}

	rscValidatingWebhookHandler, err := handlers.GetValidatingWebhookHandler(handlers.RSCValidate, RSCValidatorID, &srv.ReplicatedStorageClass{}, kwhLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating rscValidatingWebhookHandler: %s", err)
		os.Exit(1)
	}

	scValidatingWebhookHandler, err := handlers.GetValidatingWebhookHandler(handlers.SCValidate, SCValidatorID, &storagev1.StorageClass{}, kwhLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating scValidatingWebhookHandler: %s", err)
		os.Exit(1)
	}

	pvcValidatingWebhookHandler, err := handlers.GetValidatingWebhookHandler(handlers.PVCValidate, PVCValidatorID, &corev1.PersistentVolumeClaim{}, kwhLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating pvcValidatingWebhookHandler: %s", err)
		os.Exit(1)
	}

	lvgValidatingWebhookHandler, err := handlers.GetValidatingWebhookHandler(handlers.LVGValidate, LVGValidatorID, &snc.LVMVolumeGroup{}, kwhLog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating lvgValidatingWebhookHandler: %s", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/rsc-validate", rscValidatingWebhookHandler)
	mux.Handle("/sc-validate", scValidatingWebhookHandler)
	mux.Handle("/rsp-validate", rspValidatingWebhookHandler)
	mux.Handle("/pvc-validate", pvcValidatingWebhookHandler)
	mux.Handle("/lvg-validate", lvgValidatingWebhookHandler)
	mux.HandleFunc("/healthz", httpHandlerHealthz)

	log.Info("listening", "address", port)
	err = http.ListenAndServeTLS(port, cfg.certFile, cfg.keyFile, mux)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error serving webhook: %s", err)
		os.Exit(1)
	}
}
