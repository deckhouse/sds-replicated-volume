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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// statusJSONTwoPeers is a `drbdsetup status --json` dump of a three-replica
// resource whose second peer is down, plus an unrelated resource that must
// never be mistaken for it. The second resource is frozen by quorum, so the
// resource-level freeze flags are exercised on one resource without moving the
// expectations pinned on the other.
const statusJSONTwoPeers = `[
  {"name":"sdsrv-rv-1-0","node-id":0,"role":"Primary",
   "suspended":false,"suspended-user":false,"suspended-no-data":false,
   "suspended-fencing":false,"suspended-quorum":false,
   "devices":[{"volume":0,"minor":1012,"disk-state":"UpToDate","client":false,"quorum":true}],
   "connections":[
     {"peer-node-id":1,"name":"peer-1","connection-state":"Connected"},
     {"peer-node-id":2,"name":"peer-2","connection-state":"Connecting"}
   ]},
  {"name":"sdsrv-other","node-id":1,"role":"Secondary",
   "suspended":true,"suspended-user":false,"suspended-no-data":false,
   "suspended-fencing":false,"suspended-quorum":true,
   "devices":[{"volume":0,"minor":2024,"disk-state":"UpToDate","client":false,"quorum":false}],
   "connections":[{"peer-node-id":0,"name":"peer-0","connection-state":"Connected"}]}
]`

// statusJSONDevice renders a one-resource, one-device dump of sdsrv-rv-1-0 with
// the given device fields, so a test can name exactly the state it is about.
func statusJSONDevice(diskState string, deviceFields string) string {
	return fmt.Sprintf(`[{"name":"sdsrv-rv-1-0","node-id":0,"role":"Secondary",
   "suspended":false,"suspended-quorum":false,
   "devices":[{"volume":0,"minor":1012,"disk-state":%q%s}],
   "connections":[{"peer-node-id":1,"name":"peer-1","connection-state":"Connected"}]}]`,
		diskState, deviceFields)
}

// statusJSONPeerDevices is a dump whose connections carry their peer device:
// one peer fully caught up, one still carrying out-of-sync data. drbdsetup
// spells the array key with an underscore and its fields with hyphens, which is
// what makes this fixture worth having verbatim.
const statusJSONPeerDevices = `[
  {"name":"sdsrv-rv-1-0","node-id":0,"role":"Primary",
   "suspended":false,"suspended-quorum":false,
   "devices":[{"volume":0,"minor":1012,"disk-state":"UpToDate","client":false,"quorum":true}],
   "connections":[
     {"peer-node-id":1,"name":"peer-1","connection-state":"Connected",
      "peer_devices":[{"volume":0,"replication-state":"Established","peer-disk-state":"UpToDate","out-of-sync":0}]},
     {"peer-node-id":2,"name":"peer-2","connection-state":"Connected",
      "peer_devices":[{"volume":0,"replication-state":"SyncSource","peer-disk-state":"Inconsistent","out-of-sync":4096}]}
   ]}
]`

// showJSONTwoPeers is the matching `drbdsetup show --json` dump: the peer
// identity lives in connections[].net._name, the voter threshold in
// options.quorum.
const showJSONTwoPeers = `[
  {"resource":"sdsrv-rv-1-0",
   "options":{"quorum":"2","on-no-quorum":"io-error"},
   "_this_host":{"node-id":0},
   "connections":[
     {"_peer_node_id":1,"net":{"_name":"peer-1"}},
     {"_peer_node_id":2,"net":{"_name":"peer-2"}}
   ]},
  {"resource":"sdsrv-other","options":{"quorum":"majority"},"connections":[]}
]`

var _ = Describe("DRBD names", func() {
	It("prefixes the kernel resource name the way the agent does", func() {
		Expect(DRBDResourceName("rv-1-0")).To(Equal("sdsrv-rv-1-0"))
	})

	It("names a peer after its replica id", func() {
		Expect(DRBDPeerName(0)).To(Equal("peer-0"))
		Expect(DRBDPeerName(7)).To(Equal("peer-7"))
	})
})

var _ = Describe("parseDRBDStatus", func() {
	It("reads role, minor, quorum and every connection of the requested resource", func() {
		st, err := parseDRBDStatus(statusJSONTwoPeers, "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Name).To(Equal("sdsrv-rv-1-0"))
		Expect(st.Role).To(Equal("Primary"))
		Expect(st.Minor).To(Equal(1012))
		Expect(st.Quorum).To(BeTrue())
		Expect(st.PeerNames()).To(Equal([]string{"peer-1", "peer-2"}))
	})

	It("reads the disk state and the intentional-diskless flag of the device", func() {
		diskful, err := parseDRBDStatus(statusJSONTwoPeers, "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(diskful.DiskState).To(Equal("UpToDate"))
		Expect(diskful.Diskless()).To(BeFalse())
		Expect(diskful.IntentionalDiskless).To(BeFalse())

		st, err := parseDRBDStatus(statusJSONDevice("Diskless", `,"client":true,"quorum":true`), "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Diskless()).To(BeTrue())
		Expect(st.IntentionalDiskless).To(BeTrue())

		st, err = parseDRBDStatus(statusJSONDevice("Diskless", `,"client":false,"quorum":true`), "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Diskless()).To(BeTrue())
		Expect(st.IntentionalDiskless).To(BeFalse())
	})

	It("reads an absent client field as not intentional, never as intentional", func() {
		// drbdsetup omits nothing today, but a dump from an older build (or one
		// that reports the flag as "unknown") must not be read as a device that
		// gave its disk up on purpose.
		st, err := parseDRBDStatus(statusJSONDevice("Diskless", `,"quorum":true`), "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Diskless()).To(BeTrue())
		Expect(st.IntentionalDiskless).To(BeFalse())
	})

	It("reads the replication state and the out-of-sync counter of every connection", func() {
		st, err := parseDRBDStatus(statusJSONPeerDevices, "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())

		caughtUp, ok := st.Connection("peer-1")
		Expect(ok).To(BeTrue())
		Expect(caughtUp.ReplicationState).To(Equal("Established"))
		Expect(caughtUp.OutOfSyncKiB).To(Equal(0))
		Expect(caughtUp.InSync()).To(BeTrue())

		resyncing, ok := st.Connection("peer-2")
		Expect(ok).To(BeTrue())
		Expect(resyncing.ReplicationState).To(Equal("SyncSource"))
		Expect(resyncing.OutOfSyncKiB).To(Equal(4096))
		Expect(resyncing.InSync()).To(BeFalse())
	})

	It("reads an unreported out-of-sync counter as unknown, never as zero", func() {
		// A connection the node reported no peer device for says nothing about
		// convergence. Defaulting the counter to 0 would turn silence into
		// "nothing is out of sync" and make a resync assertion pass vacuously.
		st, err := parseDRBDStatus(statusJSONTwoPeers, "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())

		conn, ok := st.Connection("peer-1")
		Expect(ok).To(BeTrue())
		Expect(conn.OutOfSyncKiB).To(Equal(drbdOutOfSyncUnknown))
		Expect(conn.InSync()).To(BeFalse())
		Expect(conn.String()).To(ContainSubstring("out-of-sync=unknown"))
	})

	It("refuses a connection reporting more than one peer device", func() {
		// A resource of this module carries exactly one device, so a connection
		// with several peer devices is a resource this parser is not describing.
		dump := `[{"name":"sdsrv-rv-1-0","devices":[{"volume":0,"minor":1012}],
		  "connections":[{"peer-node-id":1,"name":"peer-1","connection-state":"Connected",
		   "peer_devices":[{"volume":0,"out-of-sync":0},{"volume":1,"out-of-sync":0}]}]}]`

		_, err := parseDRBDStatus(dump, "sdsrv-rv-1-0")

		Expect(err).To(MatchError(ContainSubstring("reports 2 peer devices")))
	})

	It("reads the I/O freeze from the resource level, not from the device", func() {
		running, err := parseDRBDStatus(statusJSONTwoPeers, "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(running.Suspended).To(BeFalse())
		Expect(running.SuspendedQuorum).To(BeFalse())

		frozen, err := parseDRBDStatus(statusJSONTwoPeers, "sdsrv-other")
		Expect(err).NotTo(HaveOccurred())
		Expect(frozen.Suspended).To(BeTrue())
		Expect(frozen.SuspendedQuorum).To(BeTrue())
	})

	It("renders the disk, the flag and the freeze into the failure text", func() {
		st, err := parseDRBDStatus(statusJSONDevice("Diskless", `,"client":false,"quorum":false`), "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.String()).To(ContainSubstring("disk=Diskless"))
		Expect(st.String()).To(ContainSubstring("client=false"))
		Expect(st.String()).To(ContainSubstring("suspended=false"))
	})

	It("separates configured peers from currently connected ones", func() {
		st, err := parseDRBDStatus(statusJSONTwoPeers, "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.ConnectedPeerNames()).To(Equal([]string{"peer-1"}))

		down, ok := st.Connection("peer-2")
		Expect(ok).To(BeTrue())
		Expect(down.PeerNodeID).To(Equal(2))
		Expect(down.Connected()).To(BeFalse())

		_, ok = st.Connection("peer-9")
		Expect(ok).To(BeFalse())
	})

	It("does not confuse resources", func() {
		st, err := parseDRBDStatus(statusJSONTwoPeers, "sdsrv-other")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Minor).To(Equal(2024))
		Expect(st.Quorum).To(BeFalse())
		Expect(st.PeerNames()).To(Equal([]string{"peer-0"}))
	})

	It("reports what the node did have when the resource is absent", func() {
		_, err := parseDRBDStatus(statusJSONTwoPeers, "sdsrv-missing")
		Expect(err).To(MatchError(ContainSubstring(`"sdsrv-missing" not found`)))
		Expect(err).To(MatchError(ContainSubstring("sdsrv-rv-1-0 sdsrv-other")))
	})

	It("fails on unparsable output instead of reporting an empty state", func() {
		_, err := parseDRBDStatus("drbdsetup: invalid option -- 'json'", "sdsrv-rv-1-0")
		Expect(err).To(MatchError(ContainSubstring("parsing drbdsetup status --json output")))
	})
})

var _ = Describe("parseDRBDConfig", func() {
	It("reads the quorum option and the configured peers", func() {
		cfg, err := parseDRBDConfig(showJSONTwoPeers, "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Name).To(Equal("sdsrv-rv-1-0"))
		Expect(cfg.Quorum).To(Equal("2"))
		Expect(cfg.PeerNames()).To(Equal([]string{"peer-1", "peer-2"}))
		Expect(cfg.Peers).To(ContainElement(DRBDConfigPeer{Name: "peer-2", PeerNodeID: 2}))
		Expect(cfg.HasPeer("peer-2")).To(BeTrue())
		Expect(cfg.HasPeer("peer-3")).To(BeFalse())
	})

	It("keeps a non-numeric quorum setting verbatim", func() {
		cfg, err := parseDRBDConfig(showJSONTwoPeers, "sdsrv-other")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Quorum).To(Equal("majority"))
		Expect(cfg.PeerNames()).To(BeEmpty())
	})

	It("reports what the node did have when the resource is absent", func() {
		_, err := parseDRBDConfig(showJSONTwoPeers, "sdsrv-missing")
		Expect(err).To(MatchError(ContainSubstring(`"sdsrv-missing" not found`)))
	})

	It("fails on unparsable output", func() {
		_, err := parseDRBDConfig("not json", "sdsrv-rv-1-0")
		Expect(err).To(MatchError(ContainSubstring("parsing drbdsetup show --json output")))
	})
})

var _ = Describe("drbdStatus / drbdConfig exec handling", func() {
	ctx := context.Background()

	It("asks the node for exactly the resource under test", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: statusJSONTwoPeers}, nil
		}}
		f := &Framework{nodeRun: stub}

		st, err := f.drbdStatus(ctx, "worker-1", "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Minor).To(Equal(1012))
		Expect(stub.calls).To(HaveLen(1))
		Expect(stub.calls[0].Node).To(Equal("worker-1"))
		Expect(stub.displays()).To(Equal([]string{"drbdsetup status --json sdsrv-rv-1-0"}))
	})

	It("turns a non-zero exit code into an error carrying stderr", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{ExitCode: 10, Stderr: "no resources defined!\n"}, nil
		}}
		f := &Framework{nodeRun: stub}

		_, err := f.drbdStatus(ctx, "worker-1", "sdsrv-rv-1-0")
		Expect(err).To(MatchError(ContainSubstring("exited with code 10")))
		Expect(err).To(MatchError(ContainSubstring("no resources defined!")))
	})

	It("propagates a transport error", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{}, errors.New("error dialing backend")
		}}
		f := &Framework{nodeRun: stub}

		_, err := f.drbdConfig(ctx, "worker-1", "sdsrv-rv-1-0")
		Expect(err).To(MatchError(ContainSubstring("error dialing backend")))
	})

	It("runs `show` for the configuration", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: showJSONTwoPeers}, nil
		}}
		f := &Framework{nodeRun: stub}

		cfg, err := f.drbdConfig(ctx, "worker-1", "sdsrv-rv-1-0")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Quorum).To(Equal("2"))
		Expect(stub.displays()).To(Equal([]string{"drbdsetup show --json sdsrv-rv-1-0"}))
	})
})

var _ = Describe("awaitIntentionalDiskless", func() {
	ctx := context.Background()

	const (
		intentional   = `,"client":true,"quorum":true`
		unintentional = `,"client":false,"quorum":true`
	)

	It("accepts a device that is diskless and flagged as an intentional client", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: statusJSONDevice("Diskless", intentional)}, nil
		}}
		f := &Framework{nodeRun: stub}

		Expect(f.awaitIntentionalDiskless(ctx, "worker-1", "sdsrv-rv-1-0",
			time.Second, time.Millisecond)).To(Succeed())
		Expect(stub.calls).To(HaveLen(1))
		Expect(stub.displays()).To(Equal([]string{"drbdsetup status --json sdsrv-rv-1-0"}))
	})

	It("rejects a diskless device that is not flagged, naming the alert it will raise", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: statusJSONDevice("Diskless", unintentional)}, nil
		}}
		f := &Framework{nodeRun: stub}

		err := f.awaitIntentionalDiskless(ctx, "worker-1", "sdsrv-rv-1-0", time.Minute, time.Millisecond)
		Expect(err).To(MatchError(ContainSubstring("NOT intentionally diskless")))
		Expect(err).To(MatchError(ContainSubstring("client:no")))
		Expect(err).To(MatchError(ContainSubstring("D8DrbdDeviceIsUnintentionalDiskless")))
		Expect(err).To(MatchError(ContainSubstring("detach --diskless")))
		Expect(err).To(MatchError(ContainSubstring(`"sdsrv-rv-1-0"`)), "the resource must be named")
		Expect(err).To(MatchError(ContainSubstring(`"worker-1"`)), "the node must be named")
		// The kernel writes the flag when the device is created or detached and
		// never revises it, so waiting out the timeout would only delay the same
		// verdict by five minutes on every affected spec.
		Expect(stub.calls).To(HaveLen(1), "an unintentionally diskless device is a terminal state")
	})

	It("is not green by default: a device with no client field at all fails", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: statusJSONDevice("Diskless", `,"quorum":true`)}, nil
		}}
		f := &Framework{nodeRun: stub}

		err := f.awaitIntentionalDiskless(ctx, "worker-1", "sdsrv-rv-1-0", time.Minute, time.Millisecond)
		Expect(err).To(MatchError(ContainSubstring("NOT intentionally diskless")))
	})

	It("waits for a detach in flight instead of demanding the disk be gone at once", func() {
		attempt := 0
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			attempt++
			if attempt < 3 {
				return ExecResult{Stdout: statusJSONDevice("UpToDate", `,"client":false,"quorum":true`)}, nil
			}
			return ExecResult{Stdout: statusJSONDevice("Diskless", intentional)}, nil
		}}
		f := &Framework{nodeRun: stub}

		Expect(f.awaitIntentionalDiskless(ctx, "worker-1", "sdsrv-rv-1-0",
			time.Minute, time.Millisecond)).To(Succeed())
		Expect(attempt).To(Equal(3))
	})

	It("keeps polling while the node does not know the resource yet", func() {
		// A replica is a datamesh member before the agent has created its minor,
		// so an unreadable answer is a state to wait out, not a verdict.
		attempt := 0
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			attempt++
			if attempt < 3 {
				return ExecResult{ExitCode: 10, Stderr: "no resources defined!"}, nil
			}
			return ExecResult{Stdout: statusJSONDevice("Diskless", intentional)}, nil
		}}
		f := &Framework{nodeRun: stub}

		Expect(f.awaitIntentionalDiskless(ctx, "worker-1", "sdsrv-rv-1-0",
			time.Minute, time.Millisecond)).To(Succeed())
		Expect(attempt).To(Equal(3))
	})

	It("times out on a device that never gives its disk up, showing the last state", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: statusJSONDevice("UpToDate", `,"client":false,"quorum":true`)}, nil
		}}
		f := &Framework{nodeRun: stub}

		err := f.awaitIntentionalDiskless(ctx, "worker-1", "sdsrv-rv-1-0",
			10*time.Millisecond, time.Millisecond)
		Expect(err).To(MatchError(ContainSubstring("timed out")))
		Expect(err).To(MatchError(ContainSubstring(`"sdsrv-rv-1-0"`)))
		Expect(err).To(MatchError(ContainSubstring(`"worker-1"`)))
		Expect(err).To(MatchError(ContainSubstring("disk=UpToDate")),
			"the failure must show what the node reports")
	})

	It("times out carrying the read failure when the node never answers", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{}, errors.New("error dialing backend")
		}}
		f := &Framework{nodeRun: stub}

		err := f.awaitIntentionalDiskless(ctx, "worker-1", "sdsrv-rv-1-0",
			10*time.Millisecond, time.Millisecond)
		Expect(err).To(MatchError(ContainSubstring("timed out")))
		Expect(err).To(MatchError(ContainSubstring("error dialing backend")))
	})

	It("stops when the context is cancelled", func() {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: statusJSONDevice("UpToDate", `,"client":false,"quorum":true`)}, nil
		}}
		f := &Framework{nodeRun: stub}

		err := f.awaitIntentionalDiskless(cancelled, "worker-1", "sdsrv-rv-1-0", time.Minute, time.Hour)
		Expect(err).To(MatchError(context.Canceled))
	})
})

var _ = Describe("awaitDRBDPeers", func() {
	ctx := context.Background()

	// showWith renders a show dump for sdsrv-rv-1-0 with the given peers.
	showWith := func(peers ...string) string {
		conns := make([]string, 0, len(peers))
		for i, p := range peers {
			conns = append(conns, fmt.Sprintf(`{"_peer_node_id":%d,"net":{"_name":%q}}`, i+1, p))
		}
		return fmt.Sprintf(`[{"resource":"sdsrv-rv-1-0","options":{"quorum":"2"},"connections":[%s]}]`,
			strings.Join(conns, ","))
	}

	It("returns as soon as the configured peer set matches, in any order", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: showWith("peer-2", "peer-1")}, nil
		}}
		f := &Framework{nodeRun: stub}

		Expect(f.awaitDRBDPeers(ctx, "worker-1", "sdsrv-rv-1-0",
			[]string{"peer-1", "peer-2"}, time.Second, time.Millisecond)).To(Succeed())
		Expect(stub.calls).To(HaveLen(1))
	})

	It("waits for a departing peer to disappear rather than demanding it at once", func() {
		attempt := 0
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			attempt++
			if attempt < 3 {
				return ExecResult{Stdout: showWith("peer-1", "peer-2")}, nil
			}
			return ExecResult{Stdout: showWith("peer-1")}, nil
		}}
		f := &Framework{nodeRun: stub}

		Expect(f.awaitDRBDPeers(ctx, "worker-1", "sdsrv-rv-1-0",
			[]string{"peer-1"}, time.Minute, time.Millisecond)).To(Succeed())
		Expect(attempt).To(Equal(3))
	})

	It("fails on an unexpected extra peer, not only on a missing one", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: showWith("peer-1", "peer-2")}, nil
		}}
		f := &Framework{nodeRun: stub}

		err := f.awaitDRBDPeers(ctx, "worker-1", "sdsrv-rv-1-0",
			[]string{"peer-1"}, 10*time.Millisecond, time.Millisecond)
		Expect(err).To(MatchError(ContainSubstring("timed out")))
		Expect(err).To(MatchError(ContainSubstring("peers [peer-1]")))
		Expect(err).To(MatchError(ContainSubstring("peer-2")), "the failure must show what the node reports")
	})

	It("gives up immediately when the node cannot be read", func() {
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{ExitCode: 10, Stderr: "no resources defined!"}, nil
		}}
		f := &Framework{nodeRun: stub}

		err := f.awaitDRBDPeers(ctx, "worker-1", "sdsrv-rv-1-0",
			[]string{"peer-1"}, time.Minute, time.Millisecond)
		Expect(err).To(MatchError(ContainSubstring("exited with code 10")))
		Expect(stub.calls).To(HaveLen(1))
	})

	It("stops when the context is cancelled", func() {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		stub := &stubRunner{respond: func(execCall) (ExecResult, error) {
			return ExecResult{Stdout: showWith("peer-1", "peer-2")}, nil
		}}
		f := &Framework{nodeRun: stub}

		err := f.awaitDRBDPeers(cancelled, "worker-1", "sdsrv-rv-1-0",
			[]string{"peer-1"}, time.Minute, time.Hour)
		Expect(err).To(MatchError(context.Canceled))
	})
})
