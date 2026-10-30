# 014 群聊数据与调度

| 项 | 内容 |
| --- | --- |
| 计划编号 | 014 |
| 状态 | 完成 |
| 更新日期 | 2026-10-30 |
| 关联需求 | FR-GRP-01、FR-GRP-03、FR-GRP-04、FR-GRP-07、FR-GRP-10、4.3 |
| 前置计划 | 010 Agent 工作区与数据层（完成）、011 助手与 Agent 模块拆分（完成） |

## 1. 简介

建立群聊领域模型：Group、Run、Task、计划与状态；群与 Run 的本地存储、计划校验与依赖图、就绪调度、Run 生命周期（规划、确认、取消、中断恢复）。不做消息总线、执行器、任务中心迁移与群聊界面。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | `internal/group/model.go`：Group/Run/Task/Plan、状态、预算与默认上限、schema；单测 | 完成 |
| T2 | `internal/group/store.go`：`groups/<id>/group.json`、`runs/<run>.json`、原子写、加载、删除与中断恢复；单测 | 完成 |
| T3 | `internal/group/plan.go`：任务校验、执行者校验、依赖成环检测、就绪集合；单测 | 完成 |
| T4 | `internal/group/scheduler.go`：就绪队列、并发 3、等待超时、取消与状态推导；单测 | 完成 |
| T5 | Run 生命周期：从目标创建、确认/放弃、取消、重启标记已中断、终态；单测 | 完成 |

## 3. 实施顺序

`T1 → T2 → T3 → T4 → T5`

模型先定；存储与状态恢复其次；计划校验先于调度；调度稳定后接完整生命周期。

## 4. 实施总结

### 产出

- T1：`internal/group/model.go` 定义 `Group`、`Run`、`Plan`、`Task`、`Report` 与 `Budget`；Run 状态覆盖 4.3 的规划中、待确认、执行中、汇总中、已完成、已失败、已取消、已中断；Task 状态覆盖等待依赖、待开始、执行中、等待回复、等待审批、已暂停、已完成、已失败、已取消，并额外保留已中断用于重启恢复。`DefaultBudget` 按 FR-GRP-07 固定成员 8、任务 12、并发 3、消息 200、等待超时 120s、单任务步数 20；ID 为 `gp-`/`r-`/`t-` 加 8 位十六进制；提供深拷贝、状态中文标签、终态判定与 Run 状态推导。
- T2：`internal/group/store.go` 以 `groups/<id>/group.json` 保存群定义、以 `runs/<run>.json` 保存 Run，均为同目录临时文件原子替换；`Load` 跳过损坏、超限、schema 不符与重复的文件且不删除；重启时把执行中或汇总中的 Run 及其未结束任务标记为已中断并写回，待确认的 Run 保持可确认；删除群时移除群目录、取消运行中的 Run、终止已中断的 Run，并保留 Run 记录与群名快照（FR-GRP-10）；助手 Run 通过空 `GroupID` 与会话 ID 归属，与其他 Run 共用 `runs/`。
- T3：`internal/group/plan.go` 校验计划模型、任务数量与任务字段，检测重复 ID、依赖不存在、自依赖、重复依赖与成环（Kahn 算法）；`ValidateAssignees` 校验执行者为可用子 Agent；`ReadyTasks` 返回依赖全部完成且处于待开始或等待依赖的任务，`BlockedReason` 识别依赖失败或取消。
- T4：`internal/group/scheduler.go` 按依赖图调度，默认并发 3，只启动依赖全部完成的任务；等待依赖超过预算的等待超时后任务失败（`wait_timeout`）并级联取消其下游；依赖失败或取消时下游任务以 `dependency_failed` / `dependency_canceled` 取消，不静默丢弃；取消时未结束任务与 Run 标记为已取消，执行中的任务等待返回且保留已产生用量；结束后按任务状态推导 Run 状态，并以任务累计值刷新 Run 用量。
- T5：`internal/group/lifecycle.go` 实现规划中的 Run 写入计划并进入待确认、确认、放弃、取消、崩溃标记已中断、恢复、终止与失败；恢复把已中断任务放回等待依赖，已完成、失败与取消的任务保持结果；终态 Run 不再变更；进入汇总与完成由 `BeginSummary` / `Finish` 提供。
- 路径：`internal/platform` 新增 `RunsDir()`，与 `GroupsDir()` 平级，供群包保存 Run。

### 自检结果

- `go build ./...` 通过。
- `gofmt -l .` 无输出。
- `go vet ./...` 通过。
- `go test ./...` 通过；新增 `model_test.go`、`store_test.go`、`plan_test.go`、`scheduler_test.go`、`lifecycle_test.go`，覆盖状态标签与终态、预算范围、群与 Run 校验、深拷贝、存储往返、损坏文件跳过、删除群保留并取消 Run、重启中断恢复、计划与成环校验、执行者校验、就绪集合、依赖顺序、并发上限、等待超时、失败级联、取消、状态推导与生命周期转换。

### 偏差与遗留

- Run 文件落在 `{DataDir}/runs/<run>.json`（与 `groups/` 平级），而非 `groups/<id>/runs/`：这样删除群目录即可删除群定义与消息，同时保留任务中心需要的 Run 记录（FR-GRP-10），助手 Run 也有统一位置；为此新增 `platform.RunsDir()`。
- 群 ID 使用 `gp-` 前缀，与旧任务组的 `g-` 区分；任务 ID 沿用 `t-`。
- Task 状态额外保留「已中断」：需求 4.3 只列出 Run 的已中断，但重启恢复需要任务级状态，恢复时放回等待依赖；任务终态严格按需求取已完成与已取消，已失败通过 `Finished` 视为本次 Run 内结束、可重试生成新任务。
- 等待超时同时用于依赖等待：默认取 Run 预算的 120s，触达后任务失败并级联取消下游；子 Agent 请求答复超时由 015 的消息总线复用同一预算。
- 群成员上限、任务上限、并发与消息上限均硬编码为 FR-GRP-07 默认值，token 预算未设（需求仍为 [TBD]），后续配置化另立计划。
- 群定义只校验成员 ID 形态（`a-` 加 8 位十六进制），是否已启用由界面在保存前按 Agent 注册表过滤（016）；`SetPlan` 不内联执行者可用性校验，调用方需在确认前调用 `ValidateAssignees`，避免群包反向依赖 `internal/agent`。
- 删除群在存储层已取消或终止相关 Run，但执行中的 Run 仍需要调用方先 `Scheduler.Cancel` 再删除，避免调度器随后用旧快照覆盖取消结果；该串联在 015 接入执行器时完成。
- 本计划为纯数据与调度层，未接入 `internal/app`、任务中心与群聊界面，无窗口验收；消息总线、执行器、汇报汇总与界面按 015、016 实施。
