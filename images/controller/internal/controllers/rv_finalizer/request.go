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

package rvfinalizer

type Request interface {
	_isRequest()
	GetRVName() string
}

//

type AddFinalizerRequest struct {
	RVName string
}

type RemoveFinalizerIfPossibleRequest struct {
	RVName string
}

// [Request] implementations
func (AddFinalizerRequest) _isRequest() {}
func (a AddFinalizerRequest) GetRVName() string {
	return a.RVName
}

func (RemoveFinalizerIfPossibleRequest) _isRequest() {}
func (r RemoveFinalizerIfPossibleRequest) GetRVName() string {
	return r.RVName
}

// ...

var _ Request = AddFinalizerRequest{}
var _ Request = RemoveFinalizerIfPossibleRequest{}

// ...
