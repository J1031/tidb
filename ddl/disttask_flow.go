// Copyright 2023 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ddl

import (
	"context"
	"encoding/json"

	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/disttask/framework/dispatcher"
	"github.com/pingcap/tidb/disttask/framework/proto"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/meta"
	"github.com/pingcap/tidb/parser/model"
)

const (
	// FlowHandleLitBackfillType is the task type to handle backfill for inject step.
	FlowHandleLitBackfillType = "FlowHandleLitBackfillType"
	// FlowHandleLitMergeType is the task type to handle backfill for merge step.
	FlowHandleLitMergeType = "FlowHandleLitMergeType"
)

// LitBackfillGlobalTaskMeta is the global task meta for lightning backfill.
type LitBackfillGlobalTaskMeta struct {
	Job        model.Job `json:"job"`
	EleID      int64     `json:"ele_id"`
	EleTypeKey []byte    `json:"ele_type_key"`
}

// LitBackfillSubTaskMeta is the subtask meta for lightning backfill.
type LitBackfillSubTaskMeta struct {
	PhysicalTableID int64 `json:"physical_table_id"`
}

type litBackfillFlowHandle struct {
	getDDL func() DDL
}

// NewLitBackfillFlowHandle creates a new litBackfillFlowHandle.
func NewLitBackfillFlowHandle(getDDL func() DDL) dispatcher.TaskFlowHandle {
	return &litBackfillFlowHandle{
		getDDL: getDDL,
	}
}

// ProcessNormalFlow processes the normal flow.
func (h *litBackfillFlowHandle) ProcessNormalFlow(_ dispatcher.Dispatch, gTask *proto.Task) (metas [][]byte, err error) {
	if gTask.State != proto.TaskStatePending {
		// This flow has only one step, finish task when it is not pending
		return nil, nil
	}

	var globalTaskMeta LitBackfillGlobalTaskMeta
	if err = json.Unmarshal(gTask.Meta, &globalTaskMeta); err != nil {
		return nil, err
	}

	d, ok := h.getDDL().(*ddl)
	if !ok {
		return nil, errors.New("The getDDL result should be the type of *ddl")
	}

	job := &globalTaskMeta.Job
	var tblInfo *model.TableInfo
	err = kv.RunInNewTxn(d.ctx, d.store, false, func(ctx context.Context, txn kv.Transaction) error {
		tblInfo, err = meta.NewMeta(txn).GetTable(job.SchemaID, job.TableID)
		return err
	})

	if tblInfo.Partition == nil {
		return nil, errors.New("Non-partition table not supported yet")
	}

	defs := tblInfo.Partition.Definitions
	physicalIDs := make([]int64, len(defs))
	for i := range defs {
		physicalIDs[i] = defs[i].ID
	}

	subTaskMetas := make([][]byte, 0, len(physicalIDs))
	for _, physicalID := range physicalIDs {
		subTaskMeta := &LitBackfillSubTaskMeta{
			PhysicalTableID: physicalID,
		}

		metaBytes, err := json.Marshal(subTaskMeta)
		if err != nil {
			return nil, err
		}

		subTaskMetas = append(subTaskMetas, metaBytes)
	}

	gTask.Step = proto.StepOne
	return subTaskMetas, nil
}

func (*litBackfillFlowHandle) ProcessErrFlow(_ dispatcher.Dispatch, _ *proto.Task, _ string) (meta []byte, err error) {
	// We do not need extra meta info when rolling back
	return nil, nil
}
