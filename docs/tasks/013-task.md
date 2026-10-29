# 013 声明式插件与本地导入

| 项 | 内容 |
| --- | --- |
| 计划编号 | 013 |
| 状态 | 完成 |
| 更新日期 | 2026-10-29 |
| 关联需求 | FR-PLG-01 至 FR-PLG-09、FR-AGT-14、FR-AGT-06、5.9 |
| 前置计划 | 011 助手与 Agent 模块拆分、012 长期记忆与按周索引（草稿） |

## 1. 简介

实现声明式插件的清单校验、全局注册表、目录与 `.bkplugin` 本地导入、每 Agent 启用清单、技能注入，以及 `fs`/`memory` 内置工具。不做插件市场、可执行代码、MCP、`group`/`delegate` 工具执行与远程下载。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | `internal/plugin/manifest.go`：schema、字段、路径与体积上限、工具白名单、失败原因；单测 | 完成 |
| T2 | `internal/plugin/store.go`：`plugins/registry.json`、安装/卸载/列表、SHA-256、同 ID 覆盖与降级拒绝、审计记录、原子写；单测 | 完成 |
| T3 | `internal/plugin/archive.go`：目录与 `.bkplugin`（未压缩 tar）导入、路径安全检查、临时文件清理；单测 | 完成 |
| T4 | 每 Agent `plugins.json` 启用清单、`fs`/`memory` 工具宿主、高风险写入审批、技能注入、token 预算；单测 | 完成 |
| T5 | 插件管理页与装配：已安装列表、本地导入、按 Agent 启用/停用、卸载确认、错误提示；新增 `ModulePlugins` 与页面测试 | 完成 |

## 3. 实施顺序

`T1 → T3 → T2 → T4 → T5`

清单契约先定；归档校验是安装的依赖，先于注册表落地；导入先于启用；工具与注入完成后再接页面与模块。（实际编码按此依赖顺序执行，与计划的 `T1 → T2 → T3` 中 T2/T3 顺序对调，交付内容一致。）

## 4. 实施总结

产出：

- `internal/plugin`：`manifest.go`（清单校验、路径安全、版本比较、工具 schema）、`archive.go`（tar/目录导入、条目与体积上限、布局白名单、SHA-256）、`store.go`（`plugins/registry.json` 原子读写、安装/升级/降级拒绝/卸载、审计日志 `audit.jsonl` 及轮转）、`inject.go`（按启用顺序注入技能与工具 schema，6000 字符预算截断）、`tools.go`（`fs.read`/`fs.write`/`memory.search` 宿主，fs 路径限制在 Agent 工作区，写入需审批回调）、`page.go`（插件管理页）。
- `internal/agent`：`plugins.go` 提供 `plugins.json` 读写与 `SkillProvider` 接口；`agentMemoryPrompt` 在记忆段后按启用顺序注入插件技能，注入失败的插件从启用清单移除并通过会话提示；助手与子 Agent 对话目标装配同步适配。
- `internal/shell`：新增 `ModulePlugins` 导航；`internal/ui` 新增插件图标；`internal/app` 新增 `plugins.go` 装配（注册表加载、启用/停用审计、技能注入适配、`CleanStaging`）。

自检结果：`go build ./...`、`gofmt -l .`、`go vet ./...`、`go test ./...` 全部通过；plugin 包新增 40+ 表驱动用例，agent 侧启用清单与注入、页面交互均有测试。

偏差与遗留：

- 工具宿主已实现并绑定工作区与记忆，但对话循环尚不派发工具调用（工具调用循环属后续计划）；`fs.write` 审批回调已预留，未接入审批界面时宿主拒绝写入（安全默认）。
- `group`/`delegate` 工具按任务边界不执行，其 schema 也不注入。
- 插件图标仅校验扩展名与体积，暂未在页面渲染；审计记录仅落盘，无查看界面（需求未要求）。
- 真机界面验证（导入对话框、启用手感、暗色主题）待用户按验证说明操作。

用户反馈修复（回归）：

- macOS 上插件页「导入目录 / 导入 .bkplugin」与应用商店安装按钮点击无反应（Windows 正常）。根因：原生对话框在主线程点击处理内同步调用时，主线程阻塞在等待对话框回调，而回调依赖的主线程 runloop source 在事件处理嵌套期间不会被服务，弹窗永远无法出现；WebApp 能力侧的文件选择因 IPC 天然在后台 goroutine 调用而不受影响。修复：插件页导入选择器、商店页安装选择器、卸载确认与错误提示弹框全部改为后台 goroutine 调用，结果经 `mygo.RunOnMain` 回主线程更新界面。

