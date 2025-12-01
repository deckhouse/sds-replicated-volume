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

package rvrdiskfulcount

type Request interface {
	_isRequest()
}

//

type AddFirstRequest struct {
	Name string
}

type AddSubsequentRequest struct {
	Name string
}

// ...

func (r AddFirstRequest) _isRequest()      {}
func (r AddSubsequentRequest) _isRequest() {}

// ...

var _ Request = AddFirstRequest{}
var _ Request = AddSubsequentRequest{}

// ...
