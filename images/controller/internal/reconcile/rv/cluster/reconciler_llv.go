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

import "fmt"

type llvReconciler struct {
	RVNodeAdapter
	llvWriter *LLVWriterImpl

	existingLLV LLVAdapter // may be nil
}

var _ diskPath = &llvReconciler{}

func newLLVReconciler(rvNode RVNodeAdapter) (*llvReconciler, error) {
	if rvNode == nil {
		return nil, errArgNil("rvNode")
	}

	llvBuilder, err := NewLLVBuilder(rvNode)
	if err != nil {
		return nil, err
	}

	res := &llvReconciler{
		RVNodeAdapter: rvNode,
		llvWriter:     llvBuilder,
	}

	return res, nil
}

func (rec *llvReconciler) hasExisting() bool {
	return rec.existingLLV != nil
}

func (rec *llvReconciler) setExistingLLV(llv LLVAdapter) error {
	if llv == nil {
		return errArgNil("llv")
	}

	if rec.existingLLV != nil {
		return errInvalidCluster(
			"expected single LLV on the node, got: %s, %s",
			rec.existingLLV.LLVName(), llv.LLVName(),
		)
	}

	if llv.LVGName() != rec.LVGName() {
		return errInvalidCluster(
			"expected llv spec.lvmVolumeGroupName to be '%s', got '%s'",
			llv.LVGName(), rec.LVGName(),
		)
	}

	rec.existingLLV = llv

	return nil
}

func (rec *llvReconciler) diskPath() string {
	return fmt.Sprintf("/dev/%s/%s", rec.LVGActualVGNameOnTheNode(), rec.actualLVNameOnTheNode())
}

func (rec *llvReconciler) initializeDynamicProps() error {
	rec.llvWriter.SetActualLVNameOnTheNode(rec.actualLVNameOnTheNode())
	return nil
}

func (rec *llvReconciler) actualLVNameOnTheNode() string {
	if rec.existingLLV == nil {
		return rec.RVName()
	}
	return rec.existingLLV.LLVActualLVNameOnTheNode()
}

func (rec *llvReconciler) reconcile() (Action, error) {
	var res Actions

	if rec.existingLLV == nil {
		res = append(
			res,
			CreateLLV{
				Writer: rec.llvWriter,
			},
		)
	} else {
		// TODO: handle error/recreate/replace scenarios
		res = append(
			res,
			PatchLLV{
				LLV:    rec.existingLLV,
				Writer: rec.llvWriter,
			},
		)
	}

	return res, nil
}
