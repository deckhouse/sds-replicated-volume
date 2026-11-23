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

package drbdr

import "testing"

type verifyAlgIntendedPeer struct {
	stubIntendedPeer
	alg string
}

func (p *verifyAlgIntendedPeer) VerifyAlg() string { return p.alg }

type verifyAlgActualPeer struct {
	stubActualPeer
	alg string
}

func (p *verifyAlgActualPeer) VerifyAlg() string { return p.alg }

func TestIntendedPeerVerifyAlgIsCRC32C(t *testing.T) {
	if got := (&intendedPeer{}).VerifyAlg(); got != "crc32c" {
		t.Fatalf("intended verify-alg = %q, want %q", got, "crc32c")
	}
}

func TestComputeNetOptionsActionReconcile_MovesPeerOffCrct10dif(t *testing.T) {
	iPeer := &verifyAlgIntendedPeer{
		stubIntendedPeer: stubIntendedPeer{name: "peer-1", nodeID: 1},
		alg:              (&intendedPeer{}).VerifyAlg(),
	}
	aPeer := &verifyAlgActualPeer{
		stubActualPeer: stubActualPeer{nodeID: 1},
		alg:            "crct10dif-pclmul",
	}

	actions := computeNetOptionsActionReconcile("sdsrv-r", iPeer, aPeer, false)
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1: %#v", len(actions), actions)
	}
	a, ok := actions[0].(NetOptionsAction)
	if !ok {
		t.Fatalf("action is %T, want NetOptionsAction", actions[0])
	}
	if a.VerifyAlg == nil || *a.VerifyAlg != "crc32c" {
		t.Fatalf("VerifyAlg = %v, want crc32c", a.VerifyAlg)
	}
	if a.Protocol != nil || a.SharedSecret != nil || a.CRAMHMACAlg != nil || a.AllowTwoPrimaries != nil || a.AllowRemoteRead != nil {
		t.Fatalf("only verify-alg must change, got %#v", a)
	}
}

func TestComputeNetOptionsActionReconcile_KeepsMatchingVerifyAlg(t *testing.T) {
	iPeer := &verifyAlgIntendedPeer{stubIntendedPeer: stubIntendedPeer{name: "peer-1", nodeID: 1}, alg: "crc32c"}
	aPeer := &verifyAlgActualPeer{stubActualPeer: stubActualPeer{nodeID: 1}, alg: "crc32c"}

	if actions := computeNetOptionsActionReconcile("sdsrv-r", iPeer, aPeer, false); len(actions) != 0 {
		t.Fatalf("got %d actions, want none: %#v", len(actions), actions)
	}
}
