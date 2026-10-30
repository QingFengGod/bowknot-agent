# 015 群聊执行与消息

| 项 | 内容 |
| --- | --- |
| 计划编号 | 015 |
| 状态 | 完成 |
| 更新日期 | 2026-10-30 |
| 关联需求 | FR-GRP-02、FR-GRP-04、FR-GRP-05、FR-GRP-06、FR-GRP-08、FR-AGT-03、FR-AGT-04、FR-AGT-05、6.3 |
| 前置计划 | 013 声明式插件与本地导入、014 群聊数据与调度（完成） |

## 1. 简介

在 Run 领域模型上实现群消息存储与总线、子 Agent 多步工具调用、进展/请求/等待/交接/阻塞、汇报与主 Agent 汇总，并把助手与群聊接到新 Run 引擎。任务中心切换与群聊界面放到 016。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | `internal/group/message.go`：消息类型、`messages.jsonl` 追加、顺序、上限与可见性；单测 | 完成 |
| T2 | `internal/group/bus.go`：广播、定向请求/答复、等待超时、主 Agent 全量可见、无私聊旁路；单测 | 完成 |
| T3 | 执行器与工具：子 Agent 多步循环（`group.post/request/wait`）、主 Agent `delegate`、步数上限、取消与工具错误；替身模型单测 | 完成 |
| T4 | Run 编排：就绪任务执行、汇报收集、主 Agent 汇总、失败说明与预算控制；单测 | 完成 |
| T5 | 接入：助手自动派发创建 Run、群聊 Run 创建、移除任务模式；群 Run 开始时接入记忆日期检查；旧任务中心适配保留到 016；单测 | 完成 |

## 3. 实施顺序

`T1 → T2 → T3 → T4 → T5`

消息落盘先定；总线依赖消息；执行器依赖总线与插件工具；编排依赖执行器；最后接助手与群聊入口。

## 4. 实施总结

### 产出

- T1：`internal/group/message.go` 定义 `Message` 与六种固定类型（进展、请求、答复、交接、阻塞、汇报）及中文标签；消息按 Run 追加到 `groups/<groupID>/messages/<runID>.jsonl`，助手 Run 写到 `runs/<runID>.jsonl`，`Seq` 在 Run 内单调递增并用于稳定排序；单条正文 2000 字、单行 64 KiB、单文件 8 MiB，超过 Run 预算 `MaxMessages`（默认 200）拒绝追加；磁盘写入失败时消息保留在内存并返回 `ErrMessageWrite`，由调用方提示（FR-GRP-02）；`Store.Load` 恢复消息与序号，损坏或非法行跳过不删除；`DeleteRun` / `DeleteGroup` 同步清理消息文件与内存。所有消息对用户、主 Agent 与全部成员可见，定向只体现在 `ToID`，没有私聊旁路。
- T2：`internal/group/bus.go` 提供 `Post`、`Request`、`Respond`、`Wait`、`WaitReply`、`Cursor` 与监听器；消息先落库再广播并唤醒匹配等待者；等待采用游标且在注册后复查已有消息，避免“先答复后等待”的漏答；等待超时返回错误，`Close` 唤醒全部等待者；请求必须带接受者，答复必须带接受者与原请求，非法定向被拒绝。
- T3：`internal/group/executor.go` 实现子 Agent 多步循环：每步只接受一个 JSON 动作，支持 `group.post/request/wait/handoff/blocked` 与插件工具；非 JSON 或 `final` 缺字段时分别按纯文本汇报或要求补全；步数上限、取消、模型错误、空回复、未知工具与插件错误都会回填说明；`group.wait` 超时作为工具失败回到模型，不丢弃任务。`internal/group/planning.go` 让主 Agent 输出 `answer` 或 `delegate`，`delegate` 校验执行者在可派发范围、依赖下标合法并创建待确认 Run；助手 Run 可派发全部启用子 Agent，群 Run 只能派发群成员。
- T4：`internal/group/manager.go` 串联确认、调度、执行、汇报与汇总；调度器按依赖与并发执行，Run 用量按任务累计；全部任务结束后主 Agent 汇总，汇总包含任务汇报、失败说明与消息记录摘录（上限 6000 字），汇总失败时使用结构化兜底并保留原因；存在失败任务时 Run 标记失败并给出失败说明；token 预算、单任务步数与消息上限触达时停止调度并记录对应错误码；取消、继续、终止、放弃均已接通；进程退出时调度器以「已中断」收尾，关闭总线并等待执行收尾后持久化，保证重启后可继续或终止（FR-AGT-07）。
- T5：`internal/agent/page.go` 增加 `RunPlanner`，助手发送消息后先由主 Agent 规划：直接回答写回会话，需要派发时创建待确认 Run 并展示计划通知，规划失败回退普通回复；生成槽通过 `ReserveExternal/ReleaseExternal` 保持单一占用。`internal/agent/memory.go` 新增按 Agent 自身 Provider/Model 生成系统提示的 `Registry.SystemPrompt`，执行器与管理器调用它注入记忆与插件提示，记忆日期检查随 Run 执行触发。`internal/ui/composer.go` 与助手页移除任务模式入口。`internal/app/group.go` 装配 Run 存储、管理器、插件工具与网关，汇总通过 `OnAssistantReply` 写回助手会话；`internal/app/app.go` 在插件装配后接入 Run 引擎，并在退出前调用 `Manager.Shutdown`。
- 测试：新增 `message_test.go`、`bus_test.go`、`executor_test.go`、`manager_test.go`、`planning` 相关用例与 `internal/app/group_test.go`，并补充助手规划、记忆系统提示用例；覆盖消息持久化与上限、写盘失败保留内存、损坏行跳过、广播与请求答复、等待超时与关闭、进展/请求/等待、步数与取消、插件工具错误、规划直接回答与派发、群成员限制、确认汇总、失败汇总与兜底、token 预算、取消与继续、助手自动派发与工具暴露。

### 自检结果

- `go build ./...` 通过。
- `gofmt -l .` 无输出。
- `go vet ./...` 通过。
- `go test ./...` 通过。

### 偏差与遗留

- 助手自动派发创建的 Run 为待确认状态，确认卡片与任务中心切换按计划留到 016；当前助手会话只显示计划通知，尚未提供确认入口。
- 插件 `fs.write` 需要审批，Run 内暂无审批交互，因此执行器只能读取与检索，写入统一返回未获批准（FR-AGT-06 后续补齐）。
- 消息上限、单任务步数与 token 预算触达时以失败说明结束 Run，未实现 FR-AGT-05 的“暂停并请求用户决定继续或终止”；014 的 Run 状态也未包含「已暂停」，需在任务中心暂停/继续交互落地时一并补齐。
- token 预算仍为 [TBD]，默认不限制；群聊页、消息流、运行中用户发言与单任务取消/重派（FR-GRP-08）留到 016 及后续计划。
- 旧任务中心继续使用 `internal/task`，与新 Run 存储并存，016 完成切换后删除。

