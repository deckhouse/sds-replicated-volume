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
	RVRName() string
	_isRequest()
}

type RVRRequest struct {
	rvrName string
}

func (r RVRRequest) RVRName() string { return r.rvrName }

//

type UpRequest struct {
	RVRRequest
}

type DownRequest struct {
	RVRRequest
}

// ...

func (UpRequest) _isRequest() {}

func (DownRequest) _isRequest() {}

// ...

var _ Request = UpRequest{}
var _ Request = DownRequest{}

// ...
