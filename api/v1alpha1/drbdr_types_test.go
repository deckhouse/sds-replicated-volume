/*
Copyright 2026 Flant JSC

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

package v1alpha1_test

import (
	// Exercise the JSON v1 encoder used by the Kubernetes client: v2 omitempty
	// semantics would hide this regression even with the old field tag.
	"encoding/json"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestDRBDResourcePeerSerializesAllowRemoteReadFalse(t *testing.T) {
	data, err := json.Marshal(v1alpha1.DRBDResourcePeer{AllowRemoteRead: false})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if got := string(fields["allowRemoteRead"]); got != "false" {
		t.Fatalf("allowRemoteRead = %q, want explicit false; JSON: %s", got, data)
	}
}
