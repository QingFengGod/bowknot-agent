# 010 Agent 工作区与数据层

| 项 | 内容 |
| --- | --- |
| 计划编号 | 010 |
| 状态 | 完成 |
| 更新日期 | 2026-10-29 |
| 关联需求 | FR-AGT-01、FR-AGT-10、FR-AGT-12、FR-AGT-15、FR-AGT-17、6.2 |
| 前置计划 | 009 任务中心最小闭环（完成；本计划起替换 008/009 的 Agent 数据实现） |

## 1. 简介

建立新的 Agent 数据层：`agents/main` 与 `agents/<id>` 工作区、`AGENTS.md`/`PERSONA.md`/`USER.md`、每 Agent 会话与记忆目录、Agent 注册表与系统提示装配骨架。主 Agent 固定不可删；子 Agent 支持创建、编辑、停用、删除、唯一名与上限。不做界面拆分、群聊、插件、记忆写入与旧数据迁移。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | `platform` 增加 `AgentsDir()`、`AgentWorkspaceDir(id)`、`GroupsDir()`、`PluginsDir()`，保留 `TaskDir()`；路径单测 | 完成 |
| T2 | `internal/agent/workspace.go`：工作区结构、默认文件、`Ensure`/`Load`、原子写、损坏回退、`AGENTS.md → PERSONA.md → USER.md` 提示装配骨架；单测 | 完成 |
| T3 | `internal/agent/registry.go`：Agent 结构、扫描 `agents/*`、主 Agent 固定、子 Agent CRUD、唯一名、上限 20、ID 生成；单测 | 完成 |
| T4 | 启动恢复：主 Agent 缺失重建、子 Agent 损坏跳过、旧 `agent/` 与 `subagents.json` 不读取；单测 | 完成 |

## 3. 实施顺序

`T1 → T2 → T3 → T4`

路径先定，工作区与注册表都依赖；工作区文件契约与提示骨架先于注册表；最后补启动恢复与旧数据隔离。

## 4. 实施总结

- 产出：`internal/platform` 新增 `AgentsDir`、`AgentWorkspaceDir`、`GroupsDir`、`PluginsDir` 与路径单测；`internal/agent/workspace.go` 定义 `agents/<id>/` 的 `AGENTS.md`、`PERSONA.md`、`USER.md`、`plugins.json`、`sessions/`、`memories/`，提供 `Ensure`（只补缺失默认文件）、`Load`（缺失回退默认、损坏保留原文件并返回 `ErrWorkspaceCorrupt`）、`Save`（原子写、UTF-8 与体积校验）与固定顺序的 `Prompt` 装配；`internal/agent/registry.go` 以 `agent.json` 记录元数据，扫描 `agents/*` 载入主 Agent 与子 Agent，提供主 Agent 固定、子 Agent 创建 / 编辑 / 停用 / 删除、大小写不敏感唯一名、上限 20、`a-xxxxxxxx` ID 生成与 `List` 主 Agent 置顶。
- 启动恢复：主 Agent 工作区或元数据缺失 / 损坏时按默认模板重建，已有 `PERSONA.md` 等文本不被覆盖；子 Agent 元数据损坏、ID 不匹配、重名或超出上限时跳过并保留目录；`agent/` 旧目录与 `subagents.json` 不读取。
- 自检：`go build ./...`、`go vet ./...`、`go test ./...` 通过，`gofmt -l .` 无输出。新增测试覆盖默认工作区、幂等 `Ensure`、提示顺序、缺失与损坏文件回退、原子保存、注册表 CRUD、主 Agent 保护、唯一名、上限、ID 校验、主 Agent 重建、子 Agent 损坏跳过与旧数据隔离。
- 偏差：工作区元数据落为 `agents/<id>/agent.json`（需求只列出文本文件与会话 / 记忆目录，元数据文件作为实现细节）；注册表不校验模型可用性，由 011 的界面与网关校验；主 Agent 默认名固定为「主 Agent」；`List` 按创建时间排序，同一秒内按 ID 稳定排序。
- 遗留：`internal/app` 装配与旧 `subagents.json` / `conversations` 读取替换放在 011；`plugins.json` 内容校验与启用清单归 013；记忆目录写入与索引归 012；本计划为纯数据层，未做窗口验收。
