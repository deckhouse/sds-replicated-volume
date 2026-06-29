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

package selftest

import (
	"flag"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
)

var (
	selfTests bool
	f         *fw.Framework
)

func init() {
	flag.BoolVar(&selfTests, "self-test", false, "run framework self-tests")
}

func TestSelfTest(t *testing.T) {
	if !selfTests {
		t.Skip("framework self-tests disabled (use -args --self-test)")
		return
	}
	f = fw.Setup()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Framework Self-Test Suite")
}
