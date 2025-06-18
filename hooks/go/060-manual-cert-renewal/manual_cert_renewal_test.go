/*
Copyright 2022 Flant JSC

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

package manualcertrenewal

import (
	"context"
	"os"
	"testing"

	"github.com/deckhouse/deckhouse/pkg/log"
	"github.com/deckhouse/module-sdk/pkg"
)

func TestManualCertRenewal(t *testing.T) {
	devMode = true
	os.Setenv("LOG_LEVEL", "INFO")

	err := manualCertRenewal(context.Background(), &pkg.HookInput{
		Logger: log.Default(),
	})

	if err != nil {
		t.Fatal(err)
	}
}
