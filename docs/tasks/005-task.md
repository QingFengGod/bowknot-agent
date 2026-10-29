# 005 App Store 本地安装、WebApp 容器与统一能力集

| 项 | 内容 |
| --- | --- |
| 计划编号 | 005 |
| 状态 | 实施中（代码完成，待人工验收） |
| 更新日期 | 2026-10-29 |
| 关联需求 | FR-APP-03 本地安装、FR-APP-04 应用清单、FR-APP-05 启动与隔离、FR-APP-06 统一能力集、FR-APP-07 卸载、FR-APP-08 崩溃隔离 |
| 前置计划 | 004 侧栏状态位、应用启动器与 App Store 骨架（已完成） |

## 1. 简介

打通 WebApp 本地闭环：安装（`.bkapp` 未压缩 tar 或本地目录）→ 启动独立无边框窗口（内嵌 webview）→ 统一能力集（私有目录、文件选择、剪贴板、通知、外链）→ 卸载。含清单 v1 校验、`bkapp://localhost` 本地协议与路径沙箱、安装索引、错误与崩溃覆盖层、启动器安装入口。不做远程商店、签名校验、应用更新、开发者模式与图标渲染。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | `platform.AppDataDir()`；`store/manifest.go` 清单 v1 结构与校验（路径、id、版本比较） | 完成 |
| T2 | `store/archive.go`：`.bkapp` tar 安全解压、目录复制、条目白名单、体积与条目上限 | 完成 |
| T3 | `store/install.go` + `registry.go`：staging 原子落盘、冲突策略、失败清理、注册表写入与启动归一 | 完成 |
| T4 | `store/protocol.go`：`bkapp://localhost/<id>/<path>` 处理器、`AppURL`、MIME、404/405、`no-store` | 完成 |
| T5 | `internal/capability/service.go`：五项能力、调用窗口身份反查、路径沙箱、令牌、结构化错误码 | 完成 |
| T6 | `internal/capability/preload.go`：`window.bowknot` 预注入脚本与错误码映射 | 完成 |
| T7 | `ui.WindowTitleBar` 通用标题栏；`store/webapp.go` 单应用单窗口、加载 / 错误 / 崩溃覆盖层 | 完成 |
| T8 | `store.Page`：安装入口、安装中状态、主操作（打开 / 启用 / 修复）、卸载入口与失败原因 | 完成 |
| T9 | `app`：注册 `bkapp` 协议、绑定能力服务、装配安装器与窗口宿主、退出关闭 | 完成 |
| T10 | 回写 requirements FR-APP-03 / 04（`.bkapp` 与 schema 1 字段）；更新本计划 | 完成 |
| T11 | 自检与人工验收步骤；提供示例目录 `docs/examples/hello-webapp/` | 完成 |

## 3. 实施顺序

`T1 → T2 → T3 → T4 → T5 → T6 → T7 → T8 → T9 → T10 → T11`

先纯逻辑与安全边界（T1–T3），安装成功是后续一切验证的前提；再做协议与能力桥（T4–T6），它们是窗口启动的前置条件；窗口与标题栏（T7）完成后接启动器交互（T8），最后装配 `app`（T9）。

## 4. 实施总结

- 交付：安装 / 卸载、WebApp 窗口、`bkapp://` 协议、五项能力与预注入、启动器安装入口、示例应用。
- 自检：`gofmt -l .` 无输出，`go vet ./...`、`go test ./...` 通过，`go tool mygo build` 成功。
- 关键约束：所有应用共用 `bkapp://localhost` origin，只承诺 `appdata/<id>` 与能力调用隔离；崩溃阈值同会话 3 次，只提示不禁用。
- 遗留：窗口与 webview 层次、拖拽缩放、真实安装包与能力集交互待人工验收；远程安装、更新检查与图标渲染留后续计划。
