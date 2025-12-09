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

package drbdconfig

type Request interface {
	_isRequest()
}

type RVRRequest interface {
	Request
	RVRName() string
}

type rvrRequest struct {
	rvrName string
}

func (r rvrRequest) RVRName() string { return r.rvrName }

//

type UpRequest struct {
	rvrRequest
}

type DownRequest struct {
	rvrRequest
}

type SharedSecretAlgRequest struct {
	RVName          string
	SharedSecretAlg string
}

// ...

func (UpRequest) _isRequest()              {}
func (DownRequest) _isRequest()            {}
func (SharedSecretAlgRequest) _isRequest() {}

// ...

var _ RVRRequest = UpRequest{}
var _ RVRRequest = DownRequest{}
var _ Request = SharedSecretAlgRequest{}

// NewUpRequest builds UpRequest for given RVR name (useful in tests).
func NewUpRequest(rvrName string) UpRequest {
	return UpRequest{rvrRequest{rvrName: rvrName}}
}

// NewDownRequest builds DownRequest for given RVR name (useful in tests).
func NewDownRequest(rvrName string) DownRequest {
	return DownRequest{rvrRequest{rvrName: rvrName}}
}

// NewSharedSecretAlgRequest builds SharedSecretAlgRequest.
func NewSharedSecretAlgRequest(rvName, alg string) SharedSecretAlgRequest {
	return SharedSecretAlgRequest{RVName: rvName, SharedSecretAlg: alg}
}

// ...
