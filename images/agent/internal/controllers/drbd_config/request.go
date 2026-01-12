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
	RVRRequestRVRName() string
}

//

type UpRequest struct {
	RVRName string
}

type DownRequest struct {
	RVRName string
}

type SharedSecretAlgRequest struct {
	RVName          string
	SharedSecretAlg string
}

// [Request] implementations

func (UpRequest) _isRequest()              {}
func (DownRequest) _isRequest()            {}
func (SharedSecretAlgRequest) _isRequest() {}

// [RVRRequest] implementations

func (r UpRequest) RVRRequestRVRName() string   { return r.RVRName }
func (r DownRequest) RVRRequestRVRName() string { return r.RVRName }

// ...

var _ RVRRequest = UpRequest{}
var _ RVRRequest = DownRequest{}
var _ Request = SharedSecretAlgRequest{}

// ...
