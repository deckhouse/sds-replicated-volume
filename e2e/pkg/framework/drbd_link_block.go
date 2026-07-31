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
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// drbdLinkBlockDir holds the watchdog's log on the node. It lives outside /run
// so a fired watchdog can still be found after the fact.
const drbdLinkBlockDir = "/var/tmp/sds-rv-e2e-netblock"

// Markers the node prints back, so an exec that produced no effect is never
// mistaken for one that did.
const (
	drbdLinkBlockProbeOK    = "#probe ok"
	drbdLinkBlockProbeNo    = "#probe missing "
	drbdLinkBlockRuleCount  = "#rules "
	drbdLinkBlockArmedMark  = "#armed"
	drbdLinkChainInput      = "INPUT"
	drbdLinkChainOutput     = "OUTPUT"
	drbdLinkBlockDeleteLoop = 200 // hard bound on the delete loop, so a node can never spin forever
)

// DRBDLinkBlockWatchdogTTL bounds how long a blockade can outlive the run that
// created it, and nothing else: in every path where the test process is still
// alive the rules are dropped by Remove or by the registered cleanup, long
// before this elapses. It is reached only when neither can run — the process was
// killed, the machine went to sleep, the cluster became unreachable — and it is
// then the sole reason the stand heals at all.
//
// Both bounds on the value are real. Too short and the watchdog fires while the
// spec is still running: the link comes back on its own, the spec observes a
// recovery it never asked for, and the failure reads as a product bug. Too long
// and a killed run leaves a stand with silently dropped DRBD traffic for that
// long. Hence a value comfortably above any spec that blocks links, and no more.
//
// INVARIANT: it MUST exceed the SpecTimeout of every spec that calls
// BlockDRBDLinks. Both sides scale with E2E_TIMEOUT_MULTIPLIER, so comparing the
// authored values is enough. It is exported for that comparison: a spec declares
// its own budget, which no policy here caps, so only the spec can check its
// budget against this one.
const DRBDLinkBlockWatchdogTTL = 40 * time.Minute

// drbdLinkBlockTagRe keeps the run tag to characters that are literal in the
// shell, in `grep -F` and in an iptables comment: it travels through all three.
var drbdLinkBlockTagRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,120}$`)

// DRBDLink is one replication link of a DRBD resource, on one system network:
// the endpoint the resource listens on locally, and the endpoint of one peer.
//
// Both ends come from the API objects (DRBDResource.status.addresses and
// DRBDResource.spec.peers[].paths[]), never from the node, which is what makes
// the blockade below unit-testable without a cluster.
type DRBDLink struct {
	// PeerName is the peer this link leads to, as DRBDResource.spec.peers
	// names it. It is carried for the failure messages only.
	PeerName string
	// SystemNetworkName is the network both endpoints live on. A resource with
	// several networks has one link per network per peer, and ALL of them have
	// to be blocked for the peer to fall silent.
	SystemNetworkName string

	LocalIP    string
	LocalPort  uint
	RemoteIP   string
	RemotePort uint
}

// String renders the link for failure messages.
func (l DRBDLink) String() string {
	return fmt.Sprintf("%s@%s %s:%d<->%s:%d",
		l.PeerName, l.SystemNetworkName, l.LocalIP, l.LocalPort, l.RemoteIP, l.RemotePort)
}

// ports renders the two endpoint ports as an iptables multiport list.
//
// `-m multiport --ports` matches a port as SOURCE or as DESTINATION, so this
// one list covers an established connection in both of its packet directions,
// no matter which side dialled: the dialling side's ephemeral port never has to
// be known.
func (l DRBDLink) ports() string {
	if l.LocalPort == l.RemotePort {
		return strconv.FormatUint(uint64(l.LocalPort), 10)
	}
	return fmt.Sprintf("%d,%d", l.LocalPort, l.RemotePort)
}

// validate refuses a link that could not produce a narrow, safe rule.
func (l DRBDLink) validate() error {
	switch {
	case l.PeerName == "":
		return errors.New("the link names no peer")
	case !isIPv4(l.LocalIP):
		return fmt.Errorf("local address %q is not an IPv4 address", l.LocalIP)
	case !isIPv4(l.RemoteIP):
		return fmt.Errorf("remote address %q is not an IPv4 address", l.RemoteIP)
	case l.LocalIP == l.RemoteIP:
		return fmt.Errorf("local and remote address are both %s, so the rule would match this node's traffic to itself", l.LocalIP)
	case l.LocalPort == 0 || l.LocalPort > 65535:
		return fmt.Errorf("local port %d is out of range", l.LocalPort)
	case l.RemotePort == 0 || l.RemotePort > 65535:
		return fmt.Errorf("remote port %d is out of range", l.RemotePort)
	}
	return nil
}

func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// DRBDLinks returns every replication link of drbdr: one per peer per system
// network, with the local end taken from status.addresses and the remote end
// from spec.peers[].paths[].
//
// It is a pure projection of the API object — nothing is read from a node — so
// a spec can compute the links before it decides to block anything.
func DRBDLinks(drbdr *v1alpha1.DRBDResource) []DRBDLink {
	GinkgoHelper()
	links, err := drbdLinks(drbdr)
	if err != nil {
		Fail(err.Error())
	}
	return links
}

// DRBDLinksToPeers is DRBDLinks restricted to the named peers, and it fails
// when one of them has no link at all — a spec that isolates a replica from
// "both peers" must not silently isolate it from one.
func DRBDLinksToPeers(drbdr *v1alpha1.DRBDResource, peerNames ...string) []DRBDLink {
	GinkgoHelper()
	links, err := drbdLinksToPeers(drbdr, peerNames)
	if err != nil {
		Fail(err.Error())
	}
	return links
}

// DRBDLinkBlock is a live blockade of DRBD replication links on one node: every
// packet of those links is dropped silently, until Remove takes the rules down.
//
// Obtain one from Framework.BlockDRBDLinks, which registers its cleanup before
// the first rule is inserted.
type DRBDLinkBlock struct {
	f        *Framework
	nodeName string
	tag      string
	links    []DRBDLink
	ttl      time.Duration
	removed  bool
}

// BlockDRBDLinks silently drops every packet of links on nodeName, and returns
// the handle that takes the rules down again.
//
// # Why a silent drop, and why iptables
//
// The point of the exercise is a REAL outage: DRBD must find out that its peer
// is gone the way it finds out in production — by its own timers expiring while
// nothing arrives. `drbdsetup disconnect` is an in-band administrative path the
// kernel knows about and acts on at once, so it never executes that timeout
// code; `-j REJECT` answers with an RST or an ICMP error, which tears the
// connection down just as promptly. Only `-j DROP` produces the silence that
// makes the peer's death a matter of timers. A future "optimisation" of either
// kind turns this into a test of the orderly path and quietly loses the
// scenario.
//
// # Why the rules are this narrow
//
// The rules pin BOTH endpoints (peer IP and this node's IP) and the ports of
// this one resource, so exactly one volume's replication breaks and the rest of
// a shared stand — other volumes, other workloads, the kubelet, the API server,
// the agent that keeps reporting status — is untouched. A rule written against
// the peer's IP alone would cut everything between the two nodes and prove far
// less than it appears to.
//
// # Guarantees
//
//   - The calling spec MUST carry LabelDisruptive, on itself or on an enclosing
//     container: this mutates the firewall of a node of a shared cluster. The
//     requirement is enforced, not merely stated — RequireDisruptiveSpec fails
//     the spec before anything is read or written.
//   - Cleanup is registered BEFORE the first rule is inserted, so no failure
//     path can leave the link blocked. Remove is idempotent, so a spec may call
//     it at the point where it expects recovery and let the cleanup find
//     nothing left to do.
//   - A watchdog on the node removes the rules by tag after
//     DRBDLinkBlockWatchdogTTL, scaled by E2E_TIMEOUT_MULTIPLIER. It is detached
//     from the exec session, so the stand heals even when the test process is
//     killed outright or the cluster becomes unreachable. The calling spec MUST
//     declare a SpecTimeout below that TTL, or the watchdog can lift the
//     blockade while the spec still relies on it; the TTL is exported so the
//     spec can assert that at compile time next to the budget it declares.
//   - Every rule carries a comment tag unique to this run, which is what makes
//     removal exact and leftovers greppable (see e2e/full/RUNNING.md).
//   - A node without iptables, or without its `comment`/`multiport` matches,
//     SKIPS the spec instead of failing it half way into a partial blockade.
func (f *Framework) BlockDRBDLinks(ctx context.Context, nodeName string, links []DRBDLink) *DRBDLinkBlock {
	GinkgoHelper()
	RequireDisruptiveSpec(fmt.Sprintf(
		"silently dropping the DRBD replication traffic of %s on node %q", describeLinks(links), nodeName))

	ttl := drbdLinkBlockTTL(timeoutMultiplier())

	b, err := f.newDRBDLinkBlock(nodeName, links, f.UniqueName()+"-netblock", ttl)
	if err != nil {
		Fail(fmt.Sprintf("blocking the DRBD links on node %q: %v", nodeName, err))
	}

	// The probe only reads, so it runs before the cleanup is registered; the
	// first rule does not.
	if err := b.probe(ctx); err != nil {
		var unsupported drbdLinkBlockUnsupportedError
		if errors.As(err, &unsupported) {
			Skip(unsupported.Error())
		}
		Fail(fmt.Sprintf("blocking the DRBD links on node %q: %v", nodeName, err))
	}

	DeferCleanup(func(cleanupCtx SpecContext) { b.Remove(cleanupCtx) })

	if err := b.apply(ctx); err != nil {
		Fail(fmt.Sprintf("blocking the DRBD links on node %q: %v", nodeName, err))
	}
	return b
}

// Tag is the iptables comment every rule of this blockade carries. It names the
// run, so leftovers are found with
// `iptables -L -n | grep <tag>` on the node.
func (b *DRBDLinkBlock) Tag() string { return b.tag }

// NodeName is the node whose firewall carries the rules.
func (b *DRBDLinkBlock) NodeName() string { return b.nodeName }

// Links are the replication links this blockade cuts.
func (b *DRBDLinkBlock) Links() []DRBDLink { return slices.Clone(b.links) }

// Remove takes every rule of this blockade down and asserts that none is left.
// It is idempotent: it is registered as the spec's cleanup and may also be
// called explicitly at the point where the spec expects the link to come back.
func (b *DRBDLinkBlock) Remove(ctx context.Context) {
	GinkgoHelper()
	if err := b.remove(ctx); err != nil {
		Fail(fmt.Sprintf("removing the DRBD link blockade %q on node %q: %v", b.tag, b.nodeName, err))
	}
}

// ---------------------------------------------------------------------------
// Core: error-returning, unit-testable with a stub runner
// ---------------------------------------------------------------------------

// drbdLinkBlockUnsupportedError says the node cannot run the scenario at all.
// It is answered with a Skip rather than a failure, because a missing firewall
// tool is a property of the stand and not of the code under test.
type drbdLinkBlockUnsupportedError struct{ msg string }

func (e drbdLinkBlockUnsupportedError) Error() string { return e.msg }

// drbdLinks projects the API object into links, one per peer per network.
func drbdLinks(drbdr *v1alpha1.DRBDResource) ([]DRBDLink, error) {
	if drbdr == nil {
		return nil, errors.New("drbd links: no DRBDResource object")
	}

	local := make(map[string]v1alpha1.DRBDAddress, len(drbdr.Status.Addresses))
	for i := range drbdr.Status.Addresses {
		a := &drbdr.Status.Addresses[i]
		local[a.SystemNetworkName] = a.Address
	}
	if len(local) == 0 {
		return nil, fmt.Errorf(
			"DRBDResource %q publishes no addresses yet, so its replication endpoints are unknown;"+
				" wait for the resource to be configured before blocking its links", drbdr.Name)
	}
	if len(drbdr.Spec.Peers) == 0 {
		return nil, fmt.Errorf("DRBDResource %q has no peers, so there is no replication link to block", drbdr.Name)
	}

	var links []DRBDLink
	for i := range drbdr.Spec.Peers {
		p := &drbdr.Spec.Peers[i]
		if len(p.Paths) == 0 {
			return nil, fmt.Errorf("peer %q of DRBDResource %q has no paths", p.Name, drbdr.Name)
		}
		for j := range p.Paths {
			path := &p.Paths[j]
			la, ok := local[path.SystemNetworkName]
			if !ok {
				return nil, fmt.Errorf(
					"peer %q of DRBDResource %q has a path on system network %q, but the resource"+
						" publishes no local address there, so that half of the link is unknown",
					p.Name, drbdr.Name, path.SystemNetworkName)
			}
			links = append(links, DRBDLink{
				PeerName:          p.Name,
				SystemNetworkName: path.SystemNetworkName,
				LocalIP:           la.IPv4,
				LocalPort:         la.Port,
				RemoteIP:          path.Address.IPv4,
				RemotePort:        path.Address.Port,
			})
		}
	}
	return links, nil
}

// drbdLinksToPeers narrows drbdLinks to the named peers and refuses a peer that
// has no link, so "isolate this replica from both of its peers" cannot degrade
// into isolating it from one.
func drbdLinksToPeers(drbdr *v1alpha1.DRBDResource, peerNames []string) ([]DRBDLink, error) {
	if len(peerNames) == 0 {
		return nil, errors.New("drbd links: no peer named")
	}
	all, err := drbdLinks(drbdr)
	if err != nil {
		return nil, err
	}

	var out []DRBDLink
	for _, want := range peerNames {
		found := 0
		for _, l := range all {
			if l.PeerName == want {
				out = append(out, l)
				found++
			}
		}
		if found == 0 {
			return nil, fmt.Errorf("DRBDResource %q has no link to peer %q (peers with links: %s)",
				drbdr.Name, want, strings.Join(linkPeerNames(all), " "))
		}
	}
	return out, nil
}

// linkPeerNames returns the sorted, de-duplicated peers the links lead to.
func linkPeerNames(links []DRBDLink) []string {
	var names []string
	for _, l := range links {
		if !slices.Contains(names, l.PeerName) {
			names = append(names, l.PeerName)
		}
	}
	slices.Sort(names)
	return names
}

// describeLinks renders the links for the guard message.
func describeLinks(links []DRBDLink) string {
	if len(links) == 0 {
		return "no links"
	}
	parts := make([]string, 0, len(links))
	for _, l := range links {
		parts = append(parts, l.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// drbdLinkBlockTTL scales the watchdog TTL by the same multiplier the framework
// applies to every SpecTimeout, so a stand slow enough to need a stretched
// budget gets a blockade that outlives it.
//
// The budget of the running spec would be the natural input here, and it is not
// available: Ginkgo enforces SpecTimeout with a timer of its own and
// deliberately does NOT build the SpecContext with context.WithDeadline (see
// NewSpecContext in ginkgo/internal), so ctx.Deadline() reports no deadline no
// matter what the spec declared. Reading it yielded a TTL that could never be
// computed in a real run — hence the constant, guarded by the invariant on it.
func drbdLinkBlockTTL(multiplier float64) time.Duration {
	return (time.Duration(float64(DRBDLinkBlockWatchdogTTL) * multiplier)).Round(time.Second)
}

// newDRBDLinkBlock validates everything that ends up inside a node command.
func (f *Framework) newDRBDLinkBlock(
	nodeName string,
	links []DRBDLink,
	tag string,
	ttl time.Duration,
) (*DRBDLinkBlock, error) {
	switch {
	case nodeName == "":
		return nil, errors.New("require: node name must not be empty")
	case len(links) == 0:
		return nil, errors.New("require: at least one link must be given, or nothing would be blocked")
	case !drbdLinkBlockTagRe.MatchString(tag):
		return nil, fmt.Errorf("require: tag %q must match %s", tag, drbdLinkBlockTagRe)
	case ttl <= 0:
		return nil, fmt.Errorf("require: the watchdog TTL must be positive, got %s", ttl)
	}
	for _, l := range links {
		if err := l.validate(); err != nil {
			return nil, fmt.Errorf("require: link %s: %w", l, err)
		}
	}
	return &DRBDLinkBlock{
		f:        f,
		nodeName: nodeName,
		tag:      tag,
		links:    slices.Clone(links),
		ttl:      ttl,
	}, nil
}

// probe checks that the node can carry the blockade at all, without changing
// anything. A missing tool is reported as drbdLinkBlockUnsupportedError so the
// caller can skip instead of failing in the middle of a partial blockade.
func (b *DRBDLinkBlock) probe(ctx context.Context) error {
	res, err := b.f.runner().HostRun(ctx, b.nodeName, drbdLinkBlockProbeCommand(), "netblock probe "+b.tag)
	if err != nil {
		return fmt.Errorf("probing iptables: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("probing iptables exited with code %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	out := res.Stdout
	if strings.Contains(out, drbdLinkBlockProbeOK) {
		return nil
	}

	i := strings.Index(out, drbdLinkBlockProbeNo)
	if i < 0 {
		// Neither verdict came back. This is NOT a skip: an unreadable probe
		// says nothing about the node, and turning it into a skip would retire
		// the spec silently — the one failure mode an opt-in class is most
		// exposed to.
		return fmt.Errorf("the iptables probe answered neither %q nor %q: %s",
			drbdLinkBlockProbeOK, strings.TrimSpace(drbdLinkBlockProbeNo),
			truncate(strings.TrimSpace(out), 256))
	}
	missing := strings.TrimSpace(firstLine(out[i+len(drbdLinkBlockProbeNo):]))

	return drbdLinkBlockUnsupportedError{msg: fmt.Sprintf(
		"node %q cannot run this spec: %s is missing. The scenario emulates a real link outage by"+
			" dropping the packets of one DRBD replication link, which needs the iptables binary and"+
			" its `comment` and `multiport` matches on the node (the nf_tables backend is fine), plus"+
			" the privileges to list and edit the filter table. Install iptables on the stand's nodes"+
			" — or run the suite on a stand that has it — and re-run; a silent packet drop cannot be"+
			" emulated without it.", b.nodeName, missing)}
}

// apply inserts every rule and then arms the watchdog.
//
// The watchdog is armed even when the insertion failed: a failed insertion is
// exactly the case where some rules may already be in place, and that is what
// the watchdog exists for. The insertion itself goes through the NO-RETRY exec:
// inserting is not idempotent, and the retrying path would duplicate rules on a
// transport error against a stale cached pod.
func (b *DRBDLinkBlock) apply(ctx context.Context) error {
	applyErr := b.insertRules(ctx)
	watchdogErr := b.armWatchdog(ctx)
	return errors.Join(applyErr, watchdogErr)
}

func (b *DRBDLinkBlock) insertRules(ctx context.Context) error {
	res, err := b.f.runner().HostRunNoRetry(ctx, b.nodeName, b.insertCommand(), "netblock apply "+b.tag)
	if err != nil {
		return fmt.Errorf("inserting the DROP rules: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("inserting the DROP rules exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	want := len(b.links) * 2 // one rule per direction
	got, err := parseDRBDLinkRuleCount(res.Stdout)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("expected %d rules tagged %q after inserting %d links, the node reports %d",
			want, b.tag, len(b.links), got)
	}

	fmt.Fprintf(GinkgoWriter, "[%s] [netblock] node=%s tag=%s blocked %s (watchdog TTL %s)\n",
		time.Now().Format("15:04:05.000"), b.nodeName, b.tag, describeLinks(b.links), b.ttl)
	return nil
}

// armWatchdog starts the detached timer that removes the rules by tag.
//
// It runs AFTER the rules are in place: the timer is the last line of defence
// for a blockade that already exists, and arming it first would only start its
// clock earlier. Arming goes through the retrying exec — a second watchdog for
// the same tag is harmless (removal by tag is idempotent), while no watchdog at
// all is not.
func (b *DRBDLinkBlock) armWatchdog(ctx context.Context) error {
	res, err := b.f.runner().HostRun(ctx, b.nodeName, b.watchdogCommand(), "netblock watchdog "+b.tag)
	if err != nil {
		return fmt.Errorf("arming the watchdog: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("arming the watchdog exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	if !strings.Contains(res.Stdout, drbdLinkBlockArmedMark) {
		return fmt.Errorf("arming the watchdog did not report %s: %s",
			drbdLinkBlockArmedMark, truncate(strings.TrimSpace(res.Stdout), 256))
	}
	return nil
}

// remove deletes every rule carrying this run's tag and verifies none is left.
//
// It is idempotent on the node (the delete loop simply finds nothing) and
// idempotent here (a completed removal is remembered), and it goes through the
// retrying exec for that reason. A removal that FAILED is deliberately not
// remembered, so the registered cleanup tries again after an explicit call
// broke down.
func (b *DRBDLinkBlock) remove(ctx context.Context) error {
	if b.removed {
		return nil
	}

	res, err := b.f.runner().HostRun(ctx, b.nodeName, b.removeCommand(), "netblock remove "+b.tag)
	if err != nil {
		return fmt.Errorf("removing the DROP rules: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("removing the DROP rules exited with code %d: %s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	left, err := parseDRBDLinkRuleCount(res.Stdout)
	if err != nil {
		return err
	}
	if left != 0 {
		return fmt.Errorf("%d rule(s) tagged %q are still in place after the removal;"+
			" the node's watchdog will drop them at the latest, and they can be found with"+
			" `iptables -L -n | grep %s`", left, b.tag, b.tag)
	}

	b.removed = true
	fmt.Fprintf(GinkgoWriter, "[%s] [netblock] node=%s tag=%s removed\n",
		time.Now().Format("15:04:05.000"), b.nodeName, b.tag)
	return nil
}

// parseDRBDLinkRuleCount reads the "#rules <n>" line the node prints after
// every mutation, which is what turns "the command exited 0" into a fact about
// the firewall.
func parseDRBDLinkRuleCount(out string) (int, error) {
	i := strings.LastIndex(out, drbdLinkBlockRuleCount)
	if i < 0 {
		return 0, fmt.Errorf("the node did not report the rule count (%s<n>): %s",
			drbdLinkBlockRuleCount, truncate(strings.TrimSpace(out), 256))
	}
	value := strings.TrimSpace(firstLine(out[i+len(drbdLinkBlockRuleCount):]))
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("the node reported an unparsable rule count %q: %w", value, err)
	}
	return n, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ---------------------------------------------------------------------------
// Node commands
// ---------------------------------------------------------------------------

// drbdLinkBlockProbeComment is the comment of the syntax probe below. Nothing
// is ever written with it — `iptables -C` only asks whether such a rule exists.
const drbdLinkBlockProbeComment = "sds-rv-e2e-netblock-probe"

// drbdLinkBlockProbeCommand reports whether the node can carry the blockade,
// without changing anything.
//
// Each match is probed with `iptables -C`, which asks whether a rule exists and
// never installs one. Its exit code is what separates the two cases: 1 means
// "no such rule", which is the expected answer and proves the syntax parsed,
// while 2 and above mean iptables refused the rule — a match it could not load.
// Probing with the very syntax the blockade uses is the point: a `--help` that
// exits differently on some build would answer a question nobody asked.
func drbdLinkBlockProbeCommand() []string {
	script := strings.Join([]string{
		`if ! command -v iptables >/dev/null 2>&1; then printf '` + drbdLinkBlockProbeNo + `%s\n' "the iptables binary"; exit 0; fi`,
		`if ! iptables -L ` + drbdLinkChainInput + ` -n >/dev/null 2>&1; then printf '` + drbdLinkBlockProbeNo + `%s\n' "access to the iptables filter table"; exit 0; fi`,
		`iptables -C ` + drbdLinkChainInput + ` -p tcp -m comment --comment "` + drbdLinkBlockProbeComment + `" -j DROP >/dev/null 2>&1; rc=$?`,
		`if [ "$rc" -gt 1 ]; then printf '` + drbdLinkBlockProbeNo + `%s\n' "the iptables comment match"; exit 0; fi`,
		`iptables -C ` + drbdLinkChainInput + ` -p tcp -m multiport --ports 1 -j DROP >/dev/null 2>&1; rc=$?`,
		`if [ "$rc" -gt 1 ]; then printf '` + drbdLinkBlockProbeNo + `%s\n' "the iptables multiport match"; exit 0; fi`,
		`printf '` + drbdLinkBlockProbeOK + `\n'`,
	}, "\n")
	return []string{"sh", "-c", script}
}

// insertCommand inserts the DROP rules and prints how many rules the node ends
// up carrying under this run's tag.
func (b *DRBDLinkBlock) insertCommand() []string {
	lines := []string{"set -e"}
	for _, l := range b.links {
		lines = append(lines,
			strings.Join(b.ruleArgs(drbdLinkChainInput, l), " "),
			strings.Join(b.ruleArgs(drbdLinkChainOutput, l), " "),
		)
	}
	lines = append(lines, b.countScript())
	return []string{"sh", "-c", strings.Join(lines, "\n")}
}

// ruleArgs builds one rule: the packets of one link, in one direction.
//
// The rule is INSERTED at the head of the chain rather than appended. A stand's
// filter table normally begins with a blanket accept for established
// connections (Cilium, kube-proxy and plain distro rules all install one), and
// an appended DROP would sit behind it and never fire on the very connection
// the spec is about — the blockade would silently do nothing.
func (b *DRBDLinkBlock) ruleArgs(chain string, l DRBDLink) []string {
	src, dst := l.RemoteIP, l.LocalIP
	if chain == drbdLinkChainOutput {
		src, dst = l.LocalIP, l.RemoteIP
	}
	return []string{
		"iptables", "-I", chain, "1",
		"-p", "tcp",
		"-s", src,
		"-d", dst,
		"-m", "multiport", "--ports", l.ports(),
		"-m", "comment", "--comment", `"` + b.tag + `"`,
		"-j", "DROP",
	}
}

// removeCommand deletes every rule carrying this run's tag, from both chains,
// and prints how many are left.
//
// Deleting by line number in a loop (rather than by re-stating the rule) is
// what makes the removal idempotent and independent of how iptables chose to
// render the rule back to us. The loop is bounded so a node can never spin on
// it, and `-n` keeps iptables from doing reverse DNS on every address, which
// on a stand without a resolver would hang the exec.
//
// The script MUST NOT contain a single quote: the watchdog embeds it verbatim
// inside `sh -c '…'`.
func (b *DRBDLinkBlock) removeCommand() []string {
	return []string{"sh", "-c", b.removeScript()}
}

func (b *DRBDLinkBlock) removeScript() string {
	return strings.Join([]string{
		`for chain in ` + drbdLinkChainInput + ` ` + drbdLinkChainOutput + `; do`,
		`  i=0`,
		`  while [ "$i" -lt ` + strconv.Itoa(drbdLinkBlockDeleteLoop) + ` ]; do`,
		`    n=$(iptables -L "$chain" -n --line-numbers 2>/dev/null | grep -F -- "` + b.tag + `" | head -n1 | cut -d" " -f1)`,
		`    if [ -z "$n" ]; then break; fi`,
		`    iptables -D "$chain" "$n" || break`,
		`    i=$((i+1))`,
		`  done`,
		`done`,
		b.countScript(),
	}, "\n")
}

// countScript prints "#rules <n>": how many rules of both chains carry this
// run's tag right now.
func (b *DRBDLinkBlock) countScript() string {
	return strings.Join([]string{
		`c=0`,
		`for chain in ` + drbdLinkChainInput + ` ` + drbdLinkChainOutput + `; do`,
		`  n=$(iptables -L "$chain" -n --line-numbers 2>/dev/null | grep -F -- "` + b.tag + `" | wc -l)`,
		`  c=$((c+n))`,
		`done`,
		`printf "` + drbdLinkBlockRuleCount + `%s\n" "$c"`,
	}, "\n")
}

// watchdogCommand starts a detached timer that removes this run's rules after
// the TTL, whatever happens to the test process.
//
// setsid + nohup + all three streams redirected is not decoration: without any
// one of them the timer dies together with the exec session that started it,
// and the blockade outlives the run it belonged to.
func (b *DRBDLinkBlock) watchdogCommand() []string {
	inner := strings.Join([]string{
		"sleep " + strconv.FormatInt(int64(b.ttl.Seconds()), 10),
		`printf "netblock watchdog fired for ` + b.tag + ` at "`,
		"date",
		b.removeScript(),
	}, "\n")

	script := strings.Join([]string{
		"set -e",
		"mkdir -p " + drbdLinkBlockDir,
		"nohup setsid sh -c '" + inner + "' </dev/null >>" + b.watchdogLogPath() + " 2>&1 &",
		"printf '" + drbdLinkBlockArmedMark + "\\n'",
	}, "\n")
	return []string{"sh", "-c", script}
}

// watchdogLogPath is where a fired watchdog leaves its trace, so a blockade
// that disappeared on its own can be explained afterwards.
func (b *DRBDLinkBlock) watchdogLogPath() string {
	return drbdLinkBlockDir + "/" + b.tag + ".watchdog.log"
}
