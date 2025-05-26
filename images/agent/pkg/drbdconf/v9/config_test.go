package v9

import (
	"os"
	"strings"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf"
	"github.com/google/go-cmp/cmp"
)

func TestMarshalUnmarshal(t *testing.T) {
	inCfg := &Config{
		Global: &Global{
			DialogRefresh:         ptr(42),
			DisableIPVerification: true,
			UsageCount:            UsageCountValueAsk,
			UdevAlwaysUseVNR:      true,
		},
		Common: &Common{
			Disk: &DiskOptions{
				ALExtents:     ptr(uint(123)),
				ALUpdates:     ptr(false),
				DiskDrain:     ptr(true),
				OnIOError:     IOErrorPolicyDetach,
				ReadBalancing: ReadBalancingPolicy64KStriping,
				ResyncAfter:   "asd/asd",
			},
		},
		Resources: []*Resource{
			{
				Name: "r1",
				Disk: &DiskOptions{
					MDFlushes: ptr(true),
				},
				Connection: []*Connection{
					{},
					{
						Name: "con1",
						Hosts: []HostAddress{
							{
								Name:    "addr1",
								Address: "123.123.124.124",
							},
							{
								Name:    "addr2",
								Address: "123.123.124.224",
							},
						},
						Paths: []*Path{
							{
								Hosts: []HostAddress{
									{
										Name:    "addr1",
										Address: "123.123.124.124",
									},
									{
										Name:    "addr2",
										Address: "123.123.124.224",
									},
								},
							},
							{},
						},
					},
				},
				On: []*On{
					{
						HostNames: []string{"h1", "h2", "h3"},
						Address: &AddressWithPort{
							AddressFamily: "ipv4",
							Address:       "123.123.123.123",
							Port:          1234,
						},
					},
					{
						HostNames: []string{"h1", "h2", "h3"},
						Address: &AddressWithPort{
							AddressFamily: "ipv4",
							Address:       "123.123.123.123",
							Port:          1234,
						},
					},
				},
				Floating: []*Floating{
					{
						NodeId: ptr(123),
						Address: &AddressWithPort{
							Address: "0.0.0.0",
							Port:    222,
						},
					},
				},
				Net: &Net{
					MaxBuffers: ptr(123),
					KOCount:    ptr(1234),
				},
				Handlers: &Handlers{
					BeforeResyncTarget: "asd",
				},
				Startup: &Startup{
					OutdatedWFCTimeout: ptr(23),
					WaitAfterSB:        true,
				},
				ConnectionMesh: &ConnectionMesh{
					Hosts: []string{"g", "h", "j"},
					Net: &Net{
						Fencing: FencingPolicyResourceAndSTONITH,
					},
				},
				Options: &Options{
					AutoPromote: ptr(true),
					PeerAckWindow: &Unit{
						Value:  5,
						Suffix: "s",
					},
					Quorum: QuorumMajority,
				},
			},
			{Name: "r2"},
		},
	}

	rootSec := &drbdconf.Section{}

	err := drbdconf.Marshal(inCfg, rootSec)
	if err != nil {
		t.Fatal(err)
	}

	root := &drbdconf.Root{}
	for _, sec := range rootSec.Elements {
		root.Elements = append(root.Elements, sec.(*drbdconf.Section))
	}

	sb := &strings.Builder{}

	_, err = root.WriteTo(sb)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n", sb.String())

	outCfg := &Config{}
	if err := drbdconf.Unmarshal(root.AsSection(), outCfg); err != nil {
		t.Fatal(err)
	}

	if !cmp.Equal(inCfg, outCfg) {
		t.Error(
			"expected inCfg to be equal to outCfg, got diff",
			"\n",
			cmp.Diff(inCfg, outCfg),
		)
	}

}

func TestUnmarshalReal(t *testing.T) {
	fsRoot, err := os.OpenRoot("./../testdata/")
	if err != nil {
		t.Fatal(err)
	}

	root, err := drbdconf.Parse(fsRoot.FS(), "root.conf")
	if err != nil {
		t.Fatal(err)
	}

	v9Conf := &Config{}

	if err := drbdconf.Unmarshal(root.AsSection(), v9Conf); err != nil {
		t.Fatal(err)
	}
}
