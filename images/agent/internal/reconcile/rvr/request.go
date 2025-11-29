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

package rvr

type Request interface {
	_isRequest()
}

// single resource was created or spec has changed
type ResourceReconcileRequest struct {
	Name string
}

var _ Request = ResourceReconcileRequest{}

func (r ResourceReconcileRequest) _isRequest() {}

// single resource was deleted and needs cleanup
type ResourceDeleteRequest struct {
	Name                 string
	ReplicatedVolumeName string
}

var _ Request = ResourceDeleteRequest{}

func (r ResourceDeleteRequest) _isRequest() {}

// special request: force primary when annotation is added
type ResourcePrimaryForceRequest struct {
	Name string
}

func (r ResourcePrimaryForceRequest) _isRequest() {}

var _ Request = ResourcePrimaryForceRequest{}

// special request: resize resource when annotation is added
type ResourceResizeRequest struct {
	Name string
}

func (r ResourceResizeRequest) _isRequest() {}

var _ Request = ResourceResizeRequest{}
