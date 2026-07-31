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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	testBlockTag    = "e2e-unit-a1-netblock"
	testBlockTTL    = 25 * time.Minute
	testLocalIP     = "10.0.0.1"
	testPeerIP      = "10.0.0.2"
	testSecondPeer  = "10.0.0.3"
	testLocalPort   = 7001
	testPeerPort    = 7002
	testSecondPort  = 7003
	testNetworkName = "default"
)

// testLink is the link every rule assertion below is written against.
func testLink() DRBDLink {
	return DRBDLink{
		PeerName:          "worker-2",
		SystemNetworkName: testNetworkName,
		LocalIP:           testLocalIP,
		LocalPort:         testLocalPort,
		RemoteIP:          testPeerIP,
		RemotePort:        testPeerPort,
	}
}

func testSecondLink() DRBDLink {
	return DRBDLink{
		PeerName:          "worker-3",
		SystemNetworkName: testNetworkName,
		LocalIP:           testLocalIP,
		LocalPort:         testLocalPort,
		RemoteIP:          testSecondPeer,
		RemotePort:        testSecondPort,
	}
}

// fakeFirewall models the node's filter table: it keeps the rules the helper
// inserted, deletes them by tag, and answers every mutation with the rule count
// the real script prints. Assertions can therefore be about the firewall's
// state, not merely about the commands that were sent.
type fakeFirewall struct {
	rules    []string
	watchdog []string

	probeOut   string
	applyExit  int
	applyErr   error
	removeExit int
	removeErr  error
	// ignoreFirst is the number of removals that leave the rules in place, and
	// swallowInserts the number of insert lines the node accepts without
	// installing anything — the shape a partially applied blockade takes.
	ignoreFirst    int
	swallowInserts int
}

func newFakeFirewall() *fakeFirewall {
	return &fakeFirewall{probeOut: drbdLinkBlockProbeOK + "\n"}
}

func (fw *fakeFirewall) respond(call execCall) (ExecResult, error) {
	script := call.Cmd[len(call.Cmd)-1]
	tag := call.Display[strings.LastIndex(call.Display, " ")+1:]

	switch {
	case strings.HasPrefix(call.Display, "netblock probe"):
		return ExecResult{Stdout: fw.probeOut}, nil

	case strings.HasPrefix(call.Display, "netblock apply"):
		if fw.applyErr != nil || fw.applyExit != 0 {
			return ExecResult{ExitCode: fw.applyExit, Stderr: "apply refused"}, fw.applyErr
		}
		for _, line := range strings.Split(script, "\n") {
			if !strings.HasPrefix(line, "iptables -I ") {
				continue
			}
			if fw.swallowInserts > 0 {
				fw.swallowInserts--
				continue
			}
			fw.rules = append(fw.rules, line)
		}
		return ExecResult{Stdout: fw.countOut(tag)}, nil

	case strings.HasPrefix(call.Display, "netblock watchdog"):
		fw.watchdog = append(fw.watchdog, script)
		return ExecResult{Stdout: drbdLinkBlockArmedMark + "\n"}, nil

	case strings.HasPrefix(call.Display, "netblock remove"):
		if fw.removeErr != nil || fw.removeExit != 0 {
			return ExecResult{ExitCode: fw.removeExit, Stderr: "remove refused"}, fw.removeErr
		}
		if fw.ignoreFirst > 0 {
			fw.ignoreFirst--
			return ExecResult{Stdout: fw.countOut(tag)}, nil
		}
		var kept []string
		for _, r := range fw.rules {
			if !strings.Contains(r, tag) {
				kept = append(kept, r)
			}
		}
		fw.rules = kept
		return ExecResult{Stdout: fw.countOut(tag)}, nil
	}

	Fail("unexpected command: " + call.Display)
	return ExecResult{}, nil
}

func (fw *fakeFirewall) countOut(tag string) string {
	n := 0
	for _, r := range fw.rules {
		if strings.Contains(r, tag) {
			n++
		}
	}
	return fmt.Sprintf("%s%d\n", drbdLinkBlockRuleCount, n)
}

// kindOfExec returns the exec kind of the first recorded call whose display
// starts with prefix. Which channel a command travels is part of its contract —
// the retrying one re-executes against a freshly resolved pod — so it is
// asserted rather than assumed.
func kindOfExec(stub *stubRunner, prefix string) string {
	GinkgoHelper()
	i := stub.indexOfDisplayPrefix(prefix)
	Expect(i).To(BeNumerically(">=", 0), "no exec with display prefix %q was recorded", prefix)
	return stub.calls[i].Kind
}

// rulesIn returns the installed rules of one chain.
func (fw *fakeFirewall) rulesIn(chain string) []string {
	var out []string
	for _, r := range fw.rules {
		if strings.HasPrefix(r, "iptables -I "+chain+" ") {
			out = append(out, r)
		}
	}
	return out
}

// newTestLinkBlock wires a blockade to the fake firewall.
func newTestLinkBlock(fw *fakeFirewall, links ...DRBDLink) (*DRBDLinkBlock, *stubRunner) {
	if len(links) == 0 {
		links = []DRBDLink{testLink()}
	}
	stub := &stubRunner{respond: fw.respond}
	f := &Framework{nodeRun: stub}
	b, err := f.newDRBDLinkBlock(testNode, links, testBlockTag, testBlockTTL)
	Expect(err).NotTo(HaveOccurred())
	return b, stub
}

var _ = Describe("DRBDLinks", func() {
	drbdrWith := func(mutate func(*v1alpha1.DRBDResource)) *v1alpha1.DRBDResource {
		d := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"},
			Spec: v1alpha1.DRBDResourceSpec{
				Peers: []v1alpha1.DRBDResourcePeer{{
					Name: "worker-2",
					Paths: []v1alpha1.DRBDResourcePath{{
						SystemNetworkName: testNetworkName,
						Address:           v1alpha1.DRBDAddress{IPv4: testPeerIP, Port: testPeerPort},
					}},
				}},
			},
			Status: v1alpha1.DRBDResourceStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{{
					SystemNetworkName: testNetworkName,
					Address:           v1alpha1.DRBDAddress{IPv4: testLocalIP, Port: testLocalPort},
				}},
			},
		}
		if mutate != nil {
			mutate(d)
		}
		return d
	}

	It("pairs the local address with the peer path of the same system network", func() {
		links, err := drbdLinks(drbdrWith(nil))

		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(HaveLen(1))
		Expect(links[0]).To(Equal(testLink()))
	})

	It("produces one link per peer per network", func() {
		d := drbdrWith(func(d *v1alpha1.DRBDResource) {
			d.Status.Addresses = append(d.Status.Addresses, v1alpha1.DRBDResourceAddressStatus{
				SystemNetworkName: "storage",
				Address:           v1alpha1.DRBDAddress{IPv4: "192.168.0.1", Port: testLocalPort},
			})
			d.Spec.Peers[0].Paths = append(d.Spec.Peers[0].Paths, v1alpha1.DRBDResourcePath{
				SystemNetworkName: "storage",
				Address:           v1alpha1.DRBDAddress{IPv4: "192.168.0.2", Port: testPeerPort},
			})
			d.Spec.Peers = append(d.Spec.Peers, v1alpha1.DRBDResourcePeer{
				Name: "worker-3",
				Paths: []v1alpha1.DRBDResourcePath{{
					SystemNetworkName: testNetworkName,
					Address:           v1alpha1.DRBDAddress{IPv4: testSecondPeer, Port: testSecondPort},
				}},
			})
		})

		links, err := drbdLinks(d)

		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(HaveLen(3))
		Expect(linkPeerNames(links)).To(Equal([]string{"worker-2", "worker-3"}))
	})

	DescribeTable("refuses an object that cannot describe a link",
		func(mutate func(*v1alpha1.DRBDResource), wantMsg string) {
			_, err := drbdLinks(drbdrWith(mutate))
			Expect(err).To(MatchError(ContainSubstring(wantMsg)))
		},
		Entry("no local addresses",
			func(d *v1alpha1.DRBDResource) { d.Status.Addresses = nil }, "publishes no addresses yet"),
		Entry("no peers",
			func(d *v1alpha1.DRBDResource) { d.Spec.Peers = nil }, "has no peers"),
		Entry("peer without paths",
			func(d *v1alpha1.DRBDResource) { d.Spec.Peers[0].Paths = nil }, "has no paths"),
		Entry("peer path on an unknown network",
			func(d *v1alpha1.DRBDResource) { d.Spec.Peers[0].Paths[0].SystemNetworkName = "storage" },
			"publishes no local address there"),
	)

	It("refuses to narrow to a peer that has no link at all", func() {
		_, err := drbdLinksToPeers(drbdrWith(nil), []string{"worker-2", "worker-9"})

		Expect(err).To(MatchError(ContainSubstring(`has no link to peer "worker-9"`)))
	})

	It("returns the links of exactly the named peers", func() {
		d := drbdrWith(func(d *v1alpha1.DRBDResource) {
			d.Spec.Peers = append(d.Spec.Peers, v1alpha1.DRBDResourcePeer{
				Name: "worker-3",
				Paths: []v1alpha1.DRBDResourcePath{{
					SystemNetworkName: testNetworkName,
					Address:           v1alpha1.DRBDAddress{IPv4: testSecondPeer, Port: testSecondPort},
				}},
			})
		})

		links, err := drbdLinksToPeers(d, []string{"worker-3"})

		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(Equal([]DRBDLink{testSecondLink()}))
	})
})

var _ = Describe("DRBDLinkBlock options", func() {
	f := &Framework{}

	DescribeTable("refuses anything that could produce an unsafe or useless rule",
		func(node string, links []DRBDLink, tag string, ttl time.Duration, wantMsg string) {
			_, err := f.newDRBDLinkBlock(node, links, tag, ttl)
			Expect(err).To(MatchError(ContainSubstring(wantMsg)))
		},
		Entry("no node", "", []DRBDLink{testLink()}, testBlockTag, testBlockTTL, "node name must not be empty"),
		Entry("no links", testNode, nil, testBlockTag, testBlockTTL, "at least one link"),
		Entry("empty tag", testNode, []DRBDLink{testLink()}, "", testBlockTTL, "tag"),
		Entry("tag with shell metacharacters", testNode, []DRBDLink{testLink()}, `x"; rm -rf /`, testBlockTTL, "tag"),
		Entry("no TTL", testNode, []DRBDLink{testLink()}, testBlockTag, time.Duration(0), "TTL must be positive"),
		Entry("local address is not an IP", testNode,
			[]DRBDLink{func() DRBDLink { l := testLink(); l.LocalIP = "worker-1"; return l }()},
			testBlockTag, testBlockTTL, "is not an IPv4 address"),
		Entry("remote address is not an IP", testNode,
			[]DRBDLink{func() DRBDLink { l := testLink(); l.RemoteIP = "10.0.0.256"; return l }()},
			testBlockTag, testBlockTTL, "is not an IPv4 address"),
		Entry("both ends are the same address", testNode,
			[]DRBDLink{func() DRBDLink { l := testLink(); l.RemoteIP = testLocalIP; return l }()},
			testBlockTag, testBlockTTL, "traffic to itself"),
		Entry("port out of range", testNode,
			[]DRBDLink{func() DRBDLink { l := testLink(); l.RemotePort = 0; return l }()},
			testBlockTag, testBlockTTL, "port 0 is out of range"),
	)
})

var _ = Describe("DRBDLinkBlock watchdog TTL", func() {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

	It("derives the TTL from the deadline the spec is actually running under", func() {
		ctx, cancel := context.WithDeadline(context.Background(), now.Add(20*time.Minute))
		defer cancel()

		ttl, err := drbdLinkBlockTTL(ctx, now)

		Expect(err).NotTo(HaveOccurred())
		Expect(ttl).To(Equal(20*time.Minute + drbdLinkBlockTTLBuffer))
	})

	It("grows with the deadline, so E2E_TIMEOUT_MULTIPLIER cannot cut the blockade short", func() {
		// The framework scales every SpecTimeout by the multiplier before the
		// spec starts, so a doubled budget arrives here as a doubled deadline —
		// and a TTL read off the deadline follows it without knowing about it.
		plain, cancelPlain := context.WithDeadline(context.Background(), now.Add(20*time.Minute))
		defer cancelPlain()
		scaled, cancelScaled := context.WithDeadline(context.Background(), now.Add(40*time.Minute))
		defer cancelScaled()

		plainTTL, err := drbdLinkBlockTTL(plain, now)
		Expect(err).NotTo(HaveOccurred())
		scaledTTL, err := drbdLinkBlockTTL(scaled, now)
		Expect(err).NotTo(HaveOccurred())

		Expect(scaledTTL).To(BeNumerically(">", plainTTL))
		Expect(scaledTTL - plainTTL).To(Equal(20 * time.Minute))
	})

	It("always outlives the window it protects", func() {
		ctx, cancel := context.WithDeadline(context.Background(), now.Add(90*time.Second))
		defer cancel()

		ttl, err := drbdLinkBlockTTL(ctx, now)

		Expect(err).NotTo(HaveOccurred())
		Expect(ttl).To(BeNumerically(">", 90*time.Second))
	})

	It("refuses a context without a deadline instead of inventing a constant", func() {
		_, err := drbdLinkBlockTTL(context.Background(), now)

		Expect(err).To(MatchError(ContainSubstring("carries no deadline")))
		Expect(err).To(MatchError(ContainSubstring("SpecTimeout")))
	})

	It("refuses a budget that has already run out", func() {
		ctx, cancel := context.WithDeadline(context.Background(), now.Add(-time.Second))
		defer cancel()

		_, err := drbdLinkBlockTTL(ctx, now)

		Expect(err).To(MatchError(ContainSubstring("budget expired")))
	})
})

var _ = Describe("DRBDLinkBlock rules", func() {
	ctx := context.Background()

	It("blocks both directions of every link", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw, testLink(), testSecondLink())

		Expect(b.apply(ctx)).To(Succeed())

		// DRBD 9 dials in both directions and whoever gets through first wins,
		// so a rule on one chain would leave the connection alive.
		Expect(fw.rulesIn(drbdLinkChainInput)).To(HaveLen(2))
		Expect(fw.rulesIn(drbdLinkChainOutput)).To(HaveLen(2))
	})

	It("narrows every rule to the address pair and the ports of that one link", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.apply(ctx)).To(Succeed())

		in := fw.rulesIn(drbdLinkChainInput)[0]
		out := fw.rulesIn(drbdLinkChainOutput)[0]
		// Inbound: from the peer to us; outbound: from us to the peer. Both
		// ends pinned, so nothing else between the two nodes is affected.
		Expect(in).To(ContainSubstring(fmt.Sprintf("-s %s -d %s", testPeerIP, testLocalIP)))
		Expect(out).To(ContainSubstring(fmt.Sprintf("-s %s -d %s", testLocalIP, testPeerIP)))
		for _, rule := range []string{in, out} {
			Expect(rule).To(ContainSubstring("-p tcp"))
			Expect(rule).To(ContainSubstring(fmt.Sprintf("-m multiport --ports %d,%d", testLocalPort, testPeerPort)))
		}
	})

	It("never writes a rule that only names the peer's address", func() {
		// A rule without -d (or without the port list) cuts everything between
		// the two nodes — other volumes, the kubelet, the agent — and would
		// prove something else entirely.
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw, testLink(), testSecondLink())

		Expect(b.apply(ctx)).To(Succeed())

		for _, rule := range fw.rules {
			Expect(rule).To(MatchRegexp(`-s \d+\.\d+\.\d+\.\d+ -d \d+\.\d+\.\d+\.\d+`),
				"rule %q does not pin both endpoints", rule)
			Expect(rule).To(ContainSubstring("--ports "), "rule %q is not narrowed to the resource's ports", rule)
		}
	})

	It("drops silently instead of rejecting, so the peer dies on DRBD's timers", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.apply(ctx)).To(Succeed())

		for _, rule := range fw.rules {
			Expect(rule).To(HaveSuffix("-j DROP"))
			Expect(rule).NotTo(ContainSubstring("REJECT"))
		}
	})

	It("inserts at the head of the chain, so a blanket accept cannot shadow the drop", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.apply(ctx)).To(Succeed())

		for _, rule := range fw.rules {
			Expect(rule).To(MatchRegexp(`^iptables -I (INPUT|OUTPUT) 1 `))
		}
	})

	It("tags every rule with this run's tag", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw, testLink(), testSecondLink())

		Expect(b.apply(ctx)).To(Succeed())

		Expect(fw.rules).To(HaveLen(4))
		for _, rule := range fw.rules {
			Expect(rule).To(ContainSubstring(`-m comment --comment "` + testBlockTag + `"`))
		}
	})

	It("inserts through the no-retry exec, and does everything else through the retrying one", func() {
		fw := newFakeFirewall()
		b, stub := newTestLinkBlock(fw)

		Expect(b.probe(ctx)).To(Succeed())
		Expect(b.apply(ctx)).To(Succeed())
		Expect(b.remove(ctx)).To(Succeed())

		// Inserting is the one step that must never run twice: HostRun
		// re-executes on a transport error against a cached pod, which would
		// install every rule a second time. The count check would then fail the
		// spec with a blockade already half applied, and the real cause — a
		// duplicated exec — would be nowhere in the report.
		Expect(kindOfExec(stub, "netblock apply")).To(Equal(execKindHostNoRetry))

		// The other three are idempotent by construction (a read-only probe, an
		// arm-by-tag, a delete-by-tag), so for them a transport error must be
		// retried rather than left half-done.
		for _, prefix := range []string{"netblock probe", "netblock watchdog", "netblock remove"} {
			Expect(kindOfExec(stub, prefix)).To(Equal(execKindHost),
				"%q must go through the retrying exec", prefix)
		}
	})

	It("gives two blockades of one run tags of their own", func() {
		f := &Framework{prefix: "e2e-unit", specCounters: map[any]int{}}
		// UniqueName without a suffix hands out one name per call, which is
		// what keeps a second blockade from being removed by the first one's
		// tag — and what makes leftovers traceable to a single run.
		Expect(f.UniqueName() + "-netblock").NotTo(Equal(f.UniqueName() + "-netblock"))
	})

	It("fails when the node ends up carrying FEWER rules than the links demanded", func() {
		// This is the direction the count exists for: a node that accepted the
		// command but installed only part of it would leave the spec asserting
		// a partial isolation it never had — the isolated replica would still
		// be talking to one of its peers, and every expectation below would be
		// about a scenario that did not happen.
		fw := newFakeFirewall()
		fw.swallowInserts = 1
		b, _ := newTestLinkBlock(fw, testLink(), testSecondLink())

		err := b.insertRules(ctx)

		Expect(err).To(MatchError(ContainSubstring("expected 4 rules")))
		Expect(err).To(MatchError(ContainSubstring("the node reports 3")))
		Expect(fw.rules).To(HaveLen(3), "the fake must model a swallowed insert, not an extra one")
	})

	It("fails when the node carries MORE rules under this tag than were inserted", func() {
		// The other direction of the same mismatch: a leftover under our own
		// tag means the removal below would be reasoning about rules it did
		// not install.
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw, testLink(), testSecondLink())
		fw.rules = append(fw.rules, `iptables -I INPUT 1 -m comment --comment "`+testBlockTag+`" -j DROP`)

		err := b.insertRules(ctx)

		Expect(err).To(MatchError(ContainSubstring("expected 4 rules")))
		Expect(err).To(MatchError(ContainSubstring("the node reports 5")))
	})
})

var _ = Describe("DRBDLinkBlock watchdog", func() {
	ctx := context.Background()

	It("arms the watchdog after the rules are in place", func() {
		fw := newFakeFirewall()
		b, stub := newTestLinkBlock(fw)

		Expect(b.apply(ctx)).To(Succeed())

		apply := stub.indexOfDisplayPrefix("netblock apply")
		watchdog := stub.indexOfDisplayPrefix("netblock watchdog")
		Expect(apply).To(BeNumerically(">=", 0))
		Expect(watchdog).To(BeNumerically(">", apply))
	})

	It("arms the watchdog even when inserting the rules failed", func() {
		// A failed insertion is precisely the case where some rules may already
		// be in place, so this is where the timer matters most.
		fw := newFakeFirewall()
		fw.applyErr = errors.New("connection reset")
		b, stub := newTestLinkBlock(fw)

		err := b.apply(ctx)

		Expect(err).To(MatchError(ContainSubstring("inserting the DROP rules")))
		Expect(stub.countDisplaysWithPrefix("netblock watchdog")).To(Equal(1))
	})

	It("gives the watchdog a TTL that outlives the spec's window", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.apply(ctx)).To(Succeed())

		Expect(fw.watchdog).To(HaveLen(1))
		Expect(fw.watchdog[0]).To(ContainSubstring(
			fmt.Sprintf("sleep %d", int64(testBlockTTL.Seconds()))))
		Expect(int64(testBlockTTL.Seconds())).To(BeNumerically(">", int64((20 * time.Minute).Seconds())))
	})

	It("detaches the watchdog from the exec session", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.apply(ctx)).To(Succeed())

		// Without any one of these the timer dies with the exec that started
		// it, and the blockade outlives the run.
		Expect(fw.watchdog[0]).To(ContainSubstring("nohup setsid sh -c "))
		Expect(fw.watchdog[0]).To(ContainSubstring("</dev/null"))
		Expect(fw.watchdog[0]).To(ContainSubstring("2>&1 &"))
	})

	It("removes by the same tag it blocked with", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.apply(ctx)).To(Succeed())

		Expect(fw.watchdog[0]).To(ContainSubstring(`grep -F -- "` + testBlockTag + `"`))
		Expect(fw.watchdog[0]).To(ContainSubstring("iptables -D "))
	})

	It("keeps the removal script free of single quotes", func() {
		// The watchdog embeds the removal script verbatim inside sh -c '…'; a
		// single quote anywhere in it would end that string and mangle the
		// command that is supposed to heal the stand.
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.removeScript()).NotTo(ContainSubstring("'"))
	})
})

var _ = Describe("DRBDLinkBlock removal", func() {
	ctx := context.Background()

	It("removes every rule of the run and verifies none is left", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw, testLink(), testSecondLink())
		Expect(b.apply(ctx)).To(Succeed())

		Expect(b.remove(ctx)).To(Succeed())

		Expect(fw.rules).To(BeEmpty())
	})

	It("leaves rules of other runs alone", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)
		Expect(b.apply(ctx)).To(Succeed())
		other := `iptables -I INPUT 1 -m comment --comment "e2e-unit-a1-other" -j DROP`
		fw.rules = append(fw.rules, other)

		Expect(b.remove(ctx)).To(Succeed())

		Expect(fw.rules).To(Equal([]string{other}))
	})

	It("is idempotent: a second removal touches the node no more", func() {
		fw := newFakeFirewall()
		b, stub := newTestLinkBlock(fw)
		Expect(b.apply(ctx)).To(Succeed())
		Expect(b.remove(ctx)).To(Succeed())
		before := stub.countDisplaysWithPrefix("netblock remove")

		Expect(b.remove(ctx)).To(Succeed())

		Expect(stub.countDisplaysWithPrefix("netblock remove")).To(Equal(before))
	})

	It("fails when the rules are still there afterwards", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)
		Expect(b.apply(ctx)).To(Succeed())
		fw.ignoreFirst = 1

		err := b.remove(ctx)

		Expect(err).To(MatchError(ContainSubstring("still in place")))
		Expect(err).To(MatchError(ContainSubstring(testBlockTag)))
	})

	It("retries a failed removal instead of remembering it as done", func() {
		// The registered cleanup is the last chance before the watchdog; a
		// removal that broke down must not have marked itself complete.
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)
		Expect(b.apply(ctx)).To(Succeed())
		fw.ignoreFirst = 1
		Expect(b.remove(ctx)).NotTo(Succeed())

		Expect(b.remove(ctx)).To(Succeed())
		Expect(fw.rules).To(BeEmpty())
	})
})

var _ = Describe("DRBDLinkBlock preflight", func() {
	ctx := context.Background()

	DescribeTable("skips the spec when the node cannot carry the blockade",
		func(probeOut, wantMissing string) {
			fw := newFakeFirewall()
			fw.probeOut = probeOut
			b, stub := newTestLinkBlock(fw)

			err := b.probe(ctx)

			var unsupported drbdLinkBlockUnsupportedError
			Expect(errors.As(err, &unsupported)).To(BeTrue(), "a missing tool must be skippable, not a failure")
			Expect(err).To(MatchError(ContainSubstring(wantMissing)))
			// The message has to say how to satisfy the precondition.
			Expect(err).To(MatchError(ContainSubstring("Install iptables")))
			Expect(stub.countDisplaysWithPrefix("netblock apply")).To(BeZero())
		},
		Entry("no binary", drbdLinkBlockProbeNo+"the iptables binary\n", "the iptables binary"),
		Entry("no comment match", drbdLinkBlockProbeNo+"the iptables comment match\n", "comment match"),
		Entry("no multiport match", drbdLinkBlockProbeNo+"the iptables multiport match\n", "multiport match"),
		Entry("no access to the table", drbdLinkBlockProbeNo+"access to the iptables filter table\n", "filter table"),
	)

	It("passes on a node that has everything", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.probe(ctx)).To(Succeed())
	})

	It("fails, rather than skips, when the probe answers nothing at all", func() {
		// An unreadable probe says nothing about the node. Skipping on it would
		// retire the spec in silence — the failure mode an opt-in class is most
		// exposed to, because it is off on most runs anyway.
		fw := newFakeFirewall()
		fw.probeOut = "bash: iptables: command substitution went sideways\n"
		b, _ := newTestLinkBlock(fw)

		err := b.probe(ctx)

		var unsupported drbdLinkBlockUnsupportedError
		Expect(errors.As(err, &unsupported)).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring("answered neither")))
	})

	It("reads nothing but the table when probing", func() {
		fw := newFakeFirewall()
		b, _ := newTestLinkBlock(fw)

		Expect(b.probe(ctx)).To(Succeed())

		Expect(fw.rules).To(BeEmpty())
	})

	It("probes the matches with the very syntax the blockade uses, and installs nothing", func() {
		script := drbdLinkBlockProbeCommand()[2]

		// -C asks whether a rule exists; only -A/-I would install one.
		Expect(script).To(ContainSubstring("-m comment --comment"))
		Expect(script).To(ContainSubstring("-m multiport --ports"))
		Expect(script).NotTo(ContainSubstring("iptables -I "))
		Expect(script).NotTo(ContainSubstring("iptables -A "))
		// Exit code 1 is "no such rule" — the expected answer, and proof that
		// the match parsed. Only 2 and above mean iptables refused it.
		Expect(script).To(ContainSubstring(`[ "$rc" -gt 1 ]`))
	})
})
