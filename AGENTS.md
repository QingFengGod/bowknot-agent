# AGENTS.md

Bowknot Agent 开发规范与协作约定的唯一入口。需求以 [docs/requirements.md](docs/requirements.md) 为准，本文件只写工程约定，不重复需求。

## 1. 项目概览

Bowknot Agent 是本地优先的桌面 AI 工作台，由四部分组成：应用商店与 WebApp 容器、助手与多 Agent 系统、创作工具、插件系统。所有 AI 能力经本地 AI 网关统一出口，数据本地优先。

产品需求、优先级与开放问题见 [docs/requirements.md](docs/requirements.md)；任务计划见 `docs/tasks/NNN-task.md`。

## 2. 已确认约束

- 不使用 Electron；桌面壳为 `mygo`。
- Go 1.27 或更高；不使用 cgo。
- 除 WebApp 窗口外，主界面、助手、创作、设置、桌宠等界面使用 mygo 原生 UI（`github.com/egoist/mygo/ui`）。
- WebApp 窗口使用系统 webview 加载网页。
- 目标平台为 macOS 与 Windows，不支持 Linux。
- 原生 UI 项目不依赖 Bun；仅在自建 Web 前端时才引入。
- 原型代码（HTML/CSS/JS）不作为实现基础，不进入本仓库。

## 3. 目录职责与依赖方向

```text
main.go                 薄入口，只装配 internal/app
internal/app            进程生命周期、主窗口装配
internal/shell          主界面导航与模块容器（FR-SHELL）
internal/platform       macOS / Windows 差异封装（托盘、快捷键、自启动、路径）
internal/store          应用商店与 WebApp 容器（FR-APP）
internal/capability     统一能力集实现与注入（FR-APP-06）
internal/agent          主 Agent / 子 Agent（FR-AGT）
internal/pet            桌宠窗口与交互（FR-PET）
internal/memory         长期记忆（FR-MEM）
internal/creation       图片 / 视频 / 语音（FR-CRE）
internal/gateway        本地 AI 网关（FR-GW）
internal/task           任务中心（FR-TASK）
internal/license        授权与订阅（FR-LIC）
internal/privacy        审计与数据管理（FR-PRIV）
internal/ui             通用布局、组件与主题
internal/settings       设置界面装配
resources/              应用图标等随包资源
docs/                   需求与任务计划
```

依赖方向：`main → internal/app → internal/<模块>`。

- 模块之间只通过显式接口或事件通信，禁止直接读写对方内部状态。
- `internal/ui` 不得依赖业务模块；业务状态由 `internal/app` 注入视图。
- 平台差异统一收敛到 `internal/platform`；WebApp 能力统一收敛到 `internal/capability`。
- 当前无对外复用需求，一律使用 `internal/`，不建 `pkg/`。

## 4. 常用命令

```bash
go tool mygo dev      # 开发运行，改 .go 文件后自动重建
go tool mygo build    # 构建，产物在 build/<os>-<arch>/
go tool mygo doctor   # 检查本机依赖
go test ./...         # 运行测试
go vet ./...          # 静态检查
gofmt -l .            # 列出未格式化文件，应无输出

# 升级 mygo CLI（go.mod 中以 tool 固定版本）
go get -tool github.com/egoist/mygo/cmd/mygo@v0.6.4
```

当前 `mygo` 固定在 `v0.6.4`，与 `go.mod` 一致。升级版本时必须同步更新这里，并重新执行本节的自检命令。

## 5. 编码规范

- `gofmt` 强制；import 分三组：标准库、第三方、本项目。
- 包名小写单词、不加下划线，目录名即包名。
- 错误用 `error` 返回，`%w` 包装并保留上下文；不用 `panic` 表达业务失败；错误分类对齐 requirements.md 6.3。
- 跨层调用传递 `context.Context`，不在结构体里缓存 `context`。
- 避免全局可变状态；依赖通过结构体字段或参数注入。
- UI 视图函数只做展示与读取状态，禁止在其中做阻塞 IO 或长任务。
- 跨 goroutine 更新界面状态必须走 mygo 提供的线程安全方式。

## 6. 测试规范

- `_test.go` 与被测包同目录。
- 优先表驱动测试。
- UI 视图用 mygo 的无窗口测试（`ui.NewTester`），点击、输入与用户操作一致。
- 外部依赖（网关、文件系统、商店源）以接口替身注入，测试不访问真实网络。
- 新增逻辑必须带测试；不设覆盖率硬门槛。
- 无法自动化的真机验收按第 11 节执行：默认交由用户验证并反馈。

## 7. 提交规范

- 使用 Conventional Commits：`feat` / `fix` / `docs` / `refactor` / `test` / `build` / `chore`。
- 单次提交单一目的；提交正文使用中文。
- 提交前保证 `gofmt -l .` 无输出、`go vet ./...` 与 `go test ./...` 通过。

## 8. 分支规范

- 主分支 `master`。
- 任务分支命名 `NNN-slug`，例如 `001-project-scaffold`。
- 任务完成后合回 `master`，不合入未通过自检的分支。

## 9. 文档规范

- 任务计划放在 `docs/tasks/NNN-task.md`，编号递增，状态字段取值为 `草稿` / `实施中` / `完成`。
- 任务计划只写四部分，不写设计规格、风险清单与验收步骤等展开内容（这些放在对话回复或需求文档中）：
  1. 简介：本次任务做什么、边界（不做什么）。
  2. 任务拆分：表格列出编号 `T1`、`T2`… 小任务与完成状态。
  3. 实施顺序：用 `T1 → T2 → …` 给出顺序，并说明关键排序理由。
  4. 实施总结：产出、自检结果、偏差与遗留。
- 新建任务沿用同一模板；任务编号与状态在实施过程中同步更新。
- 需求变更必须回写 `docs/requirements.md`，并同步受影响的计划。
- 本文件是规范唯一入口，不另建规范类文档。

## 10. 安全与隐私

- 密钥只允许保存在网关侧，且不进入版本库。
- 日志不记录密钥、令牌与完整用户正文（对齐 FR-GW-03 与 requirements.md 6.4）。
- WebApp 只能通过 `internal/capability` 访问系统能力，不暴露原始文件系统。
- 本机数据统一放在 `{UserHomeDir}/.bowknot-agent/`（macOS `~/.bowknot-agent`、Windows `C:/Users/<用户>/.bowknot-agent`），包括 `settings.json`、缓存、日志与数据库，不放仓库。路径由 `internal/platform` 提供（`DataDir()` / `SettingsPath()`），其他包不自行拼接。

## 11. UI 与功能验证

- 默认由用户验证。凡是涉及界面外观、交互手感与端到端功能的验证，代理不自行操作桌面，而是完成构建并提供可执行的验证步骤与预期结果，由用户在真实应用中操作后反馈。
- 用户没有明确要求使用 computer-use 插件时，一律按用户验证处理，不得默认开启桌面自动化；只有用户明确指定 computer-use（或明确授权代理操作桌面）时，才由代理自行操作界面并回传结果。
- 交付验证说明时需包含：验证入口（`go tool mygo dev` 或 `go tool mygo build` 的产物路径）、操作步骤、每步预期结果、需要重点观察的异常（错位、闪烁、无响应、控制台报错）。
- 能在 `ui.NewTester` 或 `go test` 中覆盖的行为不转交用户验证，只把无法自动化的视觉与真机行为交给人。
- 用户反馈的问题必须回写对应任务计划，并注明是否为回归。

## 12. 完成定义（DoD）

- `go build ./...` 通过。
- `gofmt -l .` 无输出，`go vet ./...` 与 `go test ./...` 通过。
- 相关任务计划的状态与验收结果已更新。
- 仓库内无密钥、无 `build/`、`.mygo/` 等生成物。
