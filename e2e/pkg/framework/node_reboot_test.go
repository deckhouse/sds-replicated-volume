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

package framework

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RebootNode command", func() {
	It("prints the marker and syncs before rebooting, in one invocation", func() {
		cmd := rebootCommand()

		Expect(cmd[0]).To(Equal("sh"))
		script := cmd[2]
		Expect(script).To(ContainSubstring(rebootMarker))
		Expect(script).To(ContainSubstring("sync"))
		Expect(script).To(ContainSubstring("systemctl reboot"))
		Expect(strings.Index(script, rebootMarker)).To(BeNumerically("<", strings.Index(script, "systemctl reboot")),
			"the marker must be printed before the reboot, otherwise it proves nothing")
	})
})

var _ = Describe("RebootNode outcome classification", func() {
	transport := errors.New("error dialing backend: connection reset by peer")

	DescribeTable("classifies the single reboot exec",
		func(res ExecResult, execErr error, wantErr bool, wantMsg string) {
			stub := &stubRunner{respond: func(execCall) (ExecResult, error) { return res, execErr }}
			f := &Framework{nodeRun: stub}

			err := f.rebootNodeExec(context.Background(), "worker-1")

			if wantErr {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(wantMsg))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			// The reboot must be issued exactly once in every outcome: the
			// classification alone cannot detect a retry, so count the execs.
			Expect(stub.calls).To(HaveLen(1))
			Expect(stub.calls[0].Kind).To(Equal(execKindHostNoRetry),
				"the reboot must bypass the retrying exec path")
			Expect(stub.countCommandsContaining("systemctl reboot")).To(Equal(1))
		},
		Entry("exit 0 with the marker: the reboot started",
			ExecResult{Stdout: rebootMarker + "\n"}, nil, false, ""),
		Entry("non-zero exit fails even with the marker",
			ExecResult{ExitCode: 1, Stdout: rebootMarker + "\n", Stderr: "Failed to reboot"}, nil,
			true, "exited with code 1"),
		Entry("non-zero exit without the marker fails",
			ExecResult{ExitCode: 127, Stderr: "systemctl: not found"}, nil,
			true, "exited with code 127"),
		Entry("transport error with the marker is expected: the node went down mid-exec",
			ExecResult{Stdout: rebootMarker + "\n"}, transport, false, ""),
		Entry("transport error without the marker fails: the reboot is unproven",
			ExecResult{}, transport, true, "failed before printing "+rebootMarker),
		Entry("exit 0 without the marker fails",
			ExecResult{Stdout: "\n"}, nil, true, "exited 0 without printing "+rebootMarker),
	)
})
