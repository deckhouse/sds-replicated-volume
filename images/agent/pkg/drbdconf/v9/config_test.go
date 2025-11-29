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
				Connections: []*Connection{
					{},
					{
						Name: "con1",
						Hosts: []HostAddress{
							{
								Name:            "addr1",
								AddressWithPort: "123.123.124.124:1000",
							},
							{
								Name: "addr2",
								Port: ptr[uint](1232),
							},
						},
						Paths: []*Path{
							{
								Hosts: []HostAddress{
									{
										Name:            "addr1",
										AddressWithPort: "123.123.124.124:123123",
									},
									{
										Name:            "addr2",
										AddressWithPort: "123.123.124.224",
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
						Volumes: []*Volume{
							{
								Number: ptr(0),
								Disk:   ptr(VolumeDisk("/dev/a")),
							},
							{
								Number: ptr(1),
								Disk:   ptr(VolumeDisk("/dev/b")),
							},
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
						NodeID: ptr(123),
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
					Quorum: &QuorumMajority{},
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

	dst := &drbdconf.Section{}
	if err := drbdconf.Marshal(v9Conf, dst); err != nil {
		t.Fatal(err)
	}
	dstRoot := &drbdconf.Root{}
	for _, sec := range dst.Elements {
		dstRoot.Elements = append(dstRoot.Elements, sec.(*drbdconf.Section))
	}

	sb := &strings.Builder{}

	_, err = dstRoot.WriteTo(sb)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n", sb.String())

}
