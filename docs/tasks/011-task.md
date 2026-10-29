# 011 助手与 Agent 模块拆分

| 项 | 内容 |
| --- | --- |
| 计划编号 | 011 |
| 状态 | 完成 |
| 更新日期 | 2026-10-29 |
| 关联需求 | FR-SHELL-01、FR-AGT-02、FR-AGT-11、FR-AGT-13、FR-AGT-15、FR-AGT-16、FR-AGT-18、5.3、5.4 |
| 前置计划 | 010 Agent 工作区与数据层（完成） |

## 1. 简介

把现有「助手 / Agent」单模块拆成两个一级模块：助手只保留主 Agent 会话与派发入口；Agent 页为子 Agent 卡片列表，点击卡片进入详情复用助手会话布局。Agent 页不再展示主 Agent。会话按 Agent 工作区隔离，移除旧子 Agent 列表与 `subagents.json`。任务模式暂留到 015 自动派发接入后删除。不做群聊、插件、桌宠与记忆写入。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | `shell`：新增 `ModuleAssistant`，保留 `ModuleAgent`，更新 `Modules` 顺序、图标与 `shell` 测试 | 完成 |
| T2 | 助手页：去掉子 Agent 列与切换、`CurrentAgent`；只保留主 Agent 会话、消息流、输入与任务卡片；任务模式暂留 | 完成 |
| T3 | Agent 页：子 Agent 卡片列表、点击进入详情（复用助手会话布局）、创建/编辑表单、启用/停用、删除二次确认与数据删除；不展示主 Agent；创建时可选从主 Agent 复制 `USER.md`/`PERSONA.md`；插件选择待 013 接入，本期不展示 | 完成 |
| T4 | 会话存储：按 Agent 解析 `agents/<id>/sessions/`，主/子 Agent 1:1 会话、原子写与损坏回退；单测 | 完成 |
| T5 | `internal/app` 装配：替换 `agentPaths`、`NewSubAgentStore` 与旧 `NewPage`，删除 `subagents.json` 与旧 `conversations/` 读取；更新任务中心会话跳转 | 完成 |

## 3. 实施顺序

`T1 → T4 → T2 → T3 → T5`

模块 ID 先定；会话存储被助手与 Agent 两页共同依赖，先于页面；助手页先收敛到主 Agent，再做 Agent 管理页；最后统一装配并移除旧存储。

## 4. 实施总结

### 产出

- T1：`internal/shell` 新增 `ModuleAssistant`，模块顺序为应用、助手、Agent、任务、创作、设置；新增 `assistant.svg` 与 `ui.AssistantIcon`；网关离线提示改由助手与创作页承担，Agent 页在输入区自行提示。`shell_test` 同步更新模块表与点击路径。
- T2：助手页 `agent.Page` 移除 `CurrentAgent`、子 Agent 列与切换入口，只保留主 Agent 会话、消息流、模型选择、输入区与任务卡片；会话面板保留主 Agent 会话的新建、重命名与删除。
- T3：新增 `agent.AgentPage`：默认页为子 Agent 卡片网格，卡片含头像、名称、人设摘要、模型、启用状态、会话数与操作菜单；点击卡片进入详情，详情复用助手的会话面板、消息流与输入区布局，头部提供「← 返回」与「编辑」。创建/编辑表单含名称、头像、人设、模型，新建时可选「从主 Agent 复制 USER.md、PERSONA.md」；删除走二次确认并清理工作区与会话。Agent 页不展示主 Agent，主 Agent 表单入口被拒绝。插件选择按计划不展示。
- T4：`agent.Store` 以 `agents/<id>/sessions/` 为目录，主 Agent 使用 `agents/main`，子 Agent 使用各自工作区；`ListByAgent`、`CreateFor`、`DeleteAgent` 按 Agent 隔离；加载时跳过损坏、超限、schema 不符与目录归属不符的文件且不删除；写入仍为同目录临时文件原子替换。
- T5：`internal/app/agent.go` 统一装配共享的会话存储、Agent 注册表、任务存储与 Runner；删除 `SubAgentStore`、`subagents.json` 与旧 `conversations/` 读取接口；任务中心跳转按会话所属 Agent 打开助手页或 Agent 页。

### 自检结果

- `go build ./...` 通过。
- `gofmt -l .` 无输出。
- `go vet ./...` 通过。
- `go test ./...` 通过；新增 `agentpage_test.go` 与 `app/agent_test.go`，覆盖模块拆分、表单增删改、模板复制、1:1 对话、会话隔离、删除终止任务与任务中心跳转。

### 偏差与遗留

- Agent 页按用户要求改为「卡片列表 → 详情」两级：列表页不出现主 Agent，主 Agent 的名称与人设不再从该页编辑。
- 删除子 Agent 时，若其任务处于待确认或运行中，会先取消相关任务组再删除，确认文案已明示；未采用「有运行任务即禁止删除」的额外限制。
- 助手页与 Agent 页共享同一 Runner，同一时间只允许一个回复生成，与拆分前行为一致。
- 旧 `subagents.json` 与旧 `conversations/` 不做迁移，仅忽略不读；历史数据清理留待后续数据管理任务。
- 任务模式仍保留在助手页，按计划待 015 自动派发接入后删除。
- 界面外观与真实交互需用户按 `go tool mygo dev` 验证：助手页只出现主 Agent 会话；Agent 页为子 Agent 卡片网格且无主 Agent，点击卡片进入详情、点「← 返回」回到网格；可新建、编辑、停用、删除子 Agent；子 Agent 1:1 会话与主 Agent 会话互不串扰；删除确认后会话文件与工作区一并消失。
