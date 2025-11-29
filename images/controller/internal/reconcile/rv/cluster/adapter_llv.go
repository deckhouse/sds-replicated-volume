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

package cluster

import snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"

type llvAdapter struct {
	llvName                  string
	llvActualLVNameOnTheNode string
	lvgName                  string
}

type LLVAdapter interface {
	LLVName() string
	LLVActualLVNameOnTheNode() string
	LVGName() string
}

var _ LLVAdapter = &llvAdapter{}

func NewLLVAdapter(llv *snc.LVMLogicalVolume) (*llvAdapter, error) {
	if llv == nil {
		return nil, errArgNil("llv")
	}
	llvA := &llvAdapter{
		llvName:                  llv.Name,
		lvgName:                  llv.Spec.LVMVolumeGroupName,
		llvActualLVNameOnTheNode: llv.Spec.ActualLVNameOnTheNode,
	}
	return llvA, nil
}

func (l *llvAdapter) LVGName() string {
	return l.lvgName
}

func (l *llvAdapter) LLVName() string {
	return l.llvName
}

func (l *llvAdapter) LLVActualLVNameOnTheNode() string {
	return l.llvActualLVNameOnTheNode
}
