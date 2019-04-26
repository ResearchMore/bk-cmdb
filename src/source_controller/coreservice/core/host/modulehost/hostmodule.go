/*
 * Tencent is pleased to support the open source community by making 蓝鲸 available.,
 * Copyright (C) 2017-2018 THL A29 Limited, a Tencent company. All rights reserved.
 * Licensed under the MIT License (the ",License",); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 * http://opensource.org/licenses/MIT
 * Unless required by applicable law or agreed to in writing, software distributed under
 * the License is distributed on an ",AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions and
 * limitations under the License.
 */

package modulehost

import (
	redis "gopkg.in/redis.v5"

	"configcenter/src/common"
	"configcenter/src/common/blog"
	"configcenter/src/common/condition"
	"configcenter/src/common/errors"
	"configcenter/src/common/eventclient"
	"configcenter/src/common/mapstr"
	"configcenter/src/common/metadata"
	"configcenter/src/common/util"
	"configcenter/src/source_controller/coreservice/core"
	"configcenter/src/storage/dal"
)

type ModuleHost struct {
	dbProxy dal.RDB
	eventC  eventclient.Client
	cache   *redis.Client
}

func New(db dal.RDB, cache *redis.Client, ec eventclient.Client) *ModuleHost {
	return &ModuleHost{
		dbProxy: db,
		cache:   cache,
		eventC:  ec,
	}
}

// TransferHostToInnerModule transfer host to inner module, default module contain(idle module, fault module)
func (mh *ModuleHost) TransferHostToInnerModule(ctx core.ContextParams, input *metadata.TransferHostToInnerModule) ([]metadata.ExceptionResult, error) {

	transfer := mh.NewHostModuleTransfer(ctx, input.ApplicationID, []int64{input.ModuleID}, false)

	exit, err := transfer.HasInnerModule(ctx)
	if err != nil {
		blog.ErrorJSON("TransferHostToInnerModule HasInnerModule error. err:%s, input:%s, rid:%s", err.Error(), input, ctx.ReqID)
		return nil, err
	}
	if !exit {
		blog.ErrorJSON("TransferHostToInnerModule validation module error. module ID not default. input:%s, rid:%s", input, ctx.ReqID)
		return nil, ctx.Error.CCErrorf(common.CCErrCoreServiceModuleNotDefaultModuleErr, input.ApplicationID, input.ModuleID)
	}
	err = transfer.ValidParameter(ctx)
	if err != nil {
		blog.ErrorJSON("TransferHostToInnerModule ValidParameter error. err:%s, input:%s, rid:%s", err.Error(), input, ctx.ReqID)
		return nil, err
	}

	var exceptionArr []metadata.ExceptionResult
	for _, hostID := range input.HostID {
		err := transfer.Transfer(ctx, hostID)
		if err != nil {
			blog.ErrorJSON("TransferHostToInnerModule  Transfer module host relation error. err:%s, input:%s, hostID:%s, rid:%s", err.Error(), input, hostID, ctx.ReqID)
			exceptionArr = append(exceptionArr, metadata.ExceptionResult{
				Message:     err.Error(),
				Code:        int64(err.GetCode()),
				OriginIndex: hostID,
			})
		}
	}
	if err != nil {
		return exceptionArr, ctx.Error.CCError(common.CCErrCoreServiceTransferHostModuleErr)
	}

	return nil, nil
}

// TransferHostModule transfer host to use add module
func (mh *ModuleHost) TransferHostModule(ctx core.ContextParams, input *metadata.HostsModuleRelation) ([]metadata.ExceptionResult, error) {

	transfer := mh.NewHostModuleTransfer(ctx, input.ApplicationID, input.ModuleID, input.IsIncrement)

	// 在HostModule的validParameterModule 方法中判断默认模块不可以与普通模块同时存在，
	// 是为了保证数据的证据性。 这里是为了保证逻辑的正确性。
	// 保证主机只可以在用户新加的普通模块中转移
	exit, err := transfer.HasInnerModule(ctx)
	if err != nil {
		blog.ErrorJSON("TrasferHostModule HasDefaultModule error. err:%s, input:%s, rid:%s", err.Error(), input, ctx.ReqID)
		return nil, err
	}
	if !exit {
		blog.ErrorJSON("TrasferHostModule validation module error. module ID not default. input:%s, rid:%s", input, ctx.ReqID)
		return nil, ctx.Error.CCErrorf(common.CCErrCoreServiceModuleNotDefaultModuleErr, input.ApplicationID, input.ModuleID)
	}
	err = transfer.ValidParameter(ctx)
	if err != nil {
		blog.ErrorJSON("TrasferHostModule ValidParameter error. err:%s, input:%s, rid:%s", err.Error(), input, ctx.ReqID)
		return nil, err
	}
	var exceptionArr []metadata.ExceptionResult
	for _, hostID := range input.HostID {
		err := transfer.Transfer(ctx, hostID)
		if err != nil {
			blog.ErrorJSON("TrasferHostModule  Transfer module host relation error. err:%s, input:%s, hostID:%s, rid:%s", err.Error(), input, hostID, ctx.ReqID)
			exceptionArr = append(exceptionArr, metadata.ExceptionResult{
				Message:     err.Error(),
				Code:        int64(err.GetCode()),
				OriginIndex: hostID,
			})
		}
	}
	if err != nil {
		return exceptionArr, ctx.Error.CCError(common.CCErrCoreServiceTransferHostModuleErr)
	}

	return nil, nil
}

// TransferHostCrossBusiness Host cross-business transfer
func (mh *ModuleHost) TransferHostCrossBusiness(ctx core.ContextParams, input *metadata.TransferHostsCrossBusinessRequest) ([]metadata.ExceptionResult, error) {
	transfer := mh.NewHostModuleTransfer(ctx, input.DstApplicationID, input.DstModuleIDArr, false)

	err := transfer.ValidParameter(ctx)
	if err != nil {
		blog.ErrorJSON("TransferHostCrossBusiness ValidParameter error. err:%s, input:%s, rid:%s", err.Error(), input, ctx.ReqID)
		return nil, err
	}
	var exceptionArr []metadata.ExceptionResult
	for _, hostID := range input.HostIDArr {
		err := transfer.Transfer(ctx, hostID)
		if err != nil {
			blog.ErrorJSON("TransferHostCrossBusiness  Transfer module host relation error. err:%s, input:%s, hostID:%s, rid:%s", err.Error(), input, hostID, ctx.ReqID)
			exceptionArr = append(exceptionArr, metadata.ExceptionResult{
				Message:     err.Error(),
				Code:        int64(err.GetCode()),
				OriginIndex: hostID,
			})
		}
	}
	if err != nil {
		return exceptionArr, ctx.Error.CCError(common.CCErrCoreServiceTransferHostModuleErr)
	}

	return nil, nil
}

func (mh *ModuleHost) countByCond(ctx core.ContextParams, conds mapstr.MapStr, tableName string) (uint64, errors.CCErrorCoder) {
	conds = util.SetQueryOwner(conds, ctx.SupplierAccount)
	cnt, err := mh.dbProxy.Table(tableName).Find(conds).Count(ctx)
	if err != nil {
		blog.ErrorJSON("countByCond find data error. err:%s, table:%s,cond:%s, rid:%s", err.Error(), tableName, conds, ctx.ReqID)
		return 0, ctx.Error.CCErrorf(common.CCErrCommDBSelectFailed)
	}

	return cnt, nil
}

func (mh *ModuleHost) getModuleInfoByModuleID(ctx core.ContextParams, appID int64, moduleID []int64, fields []string) ([]mapstr.MapStr, errors.CCErrorCoder) {
	moduleConds := condition.CreateCondition()
	moduleConds.Field(common.BKAppIDField).Eq(appID)
	moduleConds.Field(common.BKModuleIDField).In(moduleID)
	cond := util.SetQueryOwner(moduleConds.ToMapStr(), ctx.SupplierAccount)

	moduleInfoArr := make([]mapstr.MapStr, 0)
	err := mh.dbProxy.Table(common.BKTableNameBaseModule).Find(cond).Fields(fields...).All(ctx, &moduleInfoArr)
	if err != nil {
		blog.ErrorJSON("getModuleInfoByModuleID find data CCErrorCoder. err:%s,cond:%s, rid:%s", err.Error(), cond, ctx.ReqID)
		return nil, ctx.Error.CCErrorf(common.CCErrCommDBSelectFailed)
	}

	return moduleInfoArr, nil
}

func (mh *ModuleHost) getHostIDModuleMapByHostID(ctx core.ContextParams, appID int64, hostIDArr []int64) (map[int64][]metadata.ModuleHost, errors.CCErrorCoder) {
	moduleHostCond := condition.CreateCondition()
	moduleHostCond.Field(common.BKAppIDField).Eq(appID)
	moduleHostCond.Field(common.BKHostIDField).In(hostIDArr)
	cond := util.SetQueryOwner(moduleHostCond.ToMapStr(), ctx.SupplierAccount)

	var dataArr []metadata.ModuleHost
	err := mh.dbProxy.Table(common.BKTableNameModuleHostConfig).Find(cond).All(ctx, &dataArr)
	if err != nil {
		blog.ErrorJSON("getHostIDMOduleIDMapByHostID query db error. err:%s, cond:%#v,rid:%s", err.Error(), cond, ctx.ReqID)
		return nil, ctx.Error.CCError(common.CCErrCommDBSelectFailed)
	}
	result := make(map[int64][]metadata.ModuleHost, 0)
	for _, item := range dataArr {
		result[item.HostID] = append(result[item.HostID], item)
	}
	return result, nil
}