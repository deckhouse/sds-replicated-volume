/*
Copyright 2023 Flant JSC

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
	"net/http"
	"webhooks/validators"

	"k8s.io/klog/v2"
)

var (
	tlscert, tlskey string
)

const (
	port = ":8443"
)

func main() {
	flag.StringVar(&tlscert, "tlsCertFile", "/etc/certs/tls.crt",
		"File containing a certificate for HTTPS.")
	flag.StringVar(&tlskey, "tlsKeyFile", "/etc/certs/tls.key",
		"File containing a private key for HTTPS.")
	flag.Parse()

	http.HandleFunc("/sc-validate", validators.SCValidate)
	http.HandleFunc("/rsc-validate", validators.RSCValidate)
	http.HandleFunc("/mc-validate", validators.MCValidate)
	klog.Fatal(http.ListenAndServeTLS(port, tlscert, tlskey, nil))
}
