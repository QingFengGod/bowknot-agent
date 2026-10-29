# 011 助手与 Agent 模块拆分

| 项 | 内容 |
| --- | --- |
| 计划编号 | 011 |
| 状态 | 草稿 |
| 更新日期 | 2026-10-29 |
| 关联需求 | FR-SHELL-01、FR-AGT-02、FR-AGT-11、FR-AGT-13、FR-AGT-15、FR-AGT-16、FR-AGT-18、5.3、5.4 |
| 前置计划 | 010 Agent 工作区与数据层（完成） |

## 1. 简介

把现有「助手 / Agent」单模块拆成两个一级模块：助手只保留主 Agent 会话与派发入口；Agent 页负责主 Agent 置顶、子 Agent 管理与 1:1 会话。会话按 Agent 工作区隔离，移除旧子 Agent 列表与 `subagents.json`。任务模式暂留到 015 自动派发接入后删除。不做群聊、插件、桌宠与记忆写入。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | `shell`：新增 `ModuleAssistant`，保留 `ModuleAgent`，更新 `Modules` 顺序、图标与 `shell` 测试 | 草稿 |
| T2 | 助手页：去掉子 Agent 列与切换、`CurrentAgent`；只保留主 Agent 会话、消息流、输入与任务卡片；任务模式暂留 | 草稿 |
| T3 | Agent 页：主 Agent 置顶、子 Agent 列表、创建/编辑表单、启用/停用、删除二次确认与数据删除；创建时可选从主 Agent 复制 `USER.md`/`PERSONA.md`；插件选择待 013 接入，本期不展示 | 草稿 |
| T4 | 会话存储：按 Agent 解析 `agents/<id>/sessions/`，主/子 Agent 1:1 会话、原子写与损坏回退；单测 | 草稿 |
| T5 | `internal/app` 装配：替换 `agentPaths`、`NewSubAgentStore` 与旧 `NewPage`，删除 `subagents.json` 与旧 `conversations/` 读取；更新任务中心会话跳转 | 草稿 |

## 3. 实施顺序

`T1 → T4 → T2 → T3 → T5`

模块 ID 先定；会话存储被助手与 Agent 两页共同依赖，先于页面；助手页先收敛到主 Agent，再做 Agent 管理页；最后统一装配并移除旧存储。

## 4. 实施总结

待实施后回填产出、自检结果、偏差与遗留。
