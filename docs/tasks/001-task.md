# 001 项目搭建计划

| 项 | 内容 |
| --- | --- |
| 计划编号 | 001 |
| 状态 | 完成 |
| 更新日期 | 2026-10-26 |
| 关联需求 | requirements.md 8.1 已确认约束、8.2 mygo 能力验证项、FR-SHELL-01 |
| 目标版本 | v0.1.0-dev |
| 范围 | 工程骨架、基础目录、AGENTS.md 初始化、基础开发规范 |

## 1. 目标

### 1.1 本次交付

1. 建立可编译、可启动的 mygo 原生 UI 工程骨架（空壳主窗口）。
2. 按需求模块划分创建基础目录，明确各目录职责与依赖方向。
3. 初始化 `AGENTS.md`，作为开发规范与协作约定的唯一入口。
4. 制定基础开发规范：格式、目录边界、错误与日志、测试、提交、文档与计划编号规则。

### 1.2 非目标

- 不实现任何业务功能（商店、Agent、创作、网关、授权等均只建目录）。
- 不实现 HTML 原型，也不把原型代码放入本仓库（见 requirements.md 8.3）。
- 不引入 Web 前端工程（原生 UI 使用 Go；WebApp 页面由第三方应用自带）。
- 不做打包签名、公证、自动更新配置（需证书与 macOS 环境，后续计划处理）。
- 不引入 README、LICENSE、CI（本次不创建文档类附加文件，[待确认]）。

## 2. 前置条件与环境结论

| 项 | 实测 / 结论 |
| --- | --- |
| Go | `go1.27.2 windows/amd64`，满足 requirements.md 的 Go ≥ 1.27 |
| mygo CLI | 实际固定 `0.5.5`（本机无外网，为模块缓存可用的最高版本；`0.6.4` 待网络恢复后升级），以 `go tool` 固定 |
| Bun | `1.4.0` 已安装；原生 UI 不依赖，仅保留环境 |
| 仓库状态 | 分支 `master`，无远端、无初始提交；已有 `docs/requirements.md` |
| 平台 | 本机 Windows 可验证 Windows 构建与运行；macOS 构建/签名需在 Mac 上补验 |
| macOS 版本 | mygo 要求 macOS 12+；Windows 10/11，WebView2（Windows 11 自带） |

mygo 官方骨架结论（用于对齐目录与命令，来源：mygo 文档 getting-started / configuration）：

- 原生 UI 项目为单一 Go module，入口为**根目录 `main.go`（package main）**，配置为 `mygo.json`，图标在 `resources/icon.png`。
- 开发运行 `go tool mygo dev`，构建 `go tool mygo build`，产物在 `build/<os>-<arch>/`。
- CLI 以 Go tool 形式固定版本：`go get -tool github.com/egoist/mygo/cmd/mygo@v0.6.4`。
- 配置不含入口路径字段，因此不采用 `cmd/` 布局，根 `main.go` 只做装配。

## 3. 技术决策（本次锁定）

| 决策 | 结论 | 理由 |
| --- | --- | --- |
| Module 路径 | `github.com/bowknot/bowknot-agent` [待确认] | 无远端仓库，先按项目名占位；确定后全局替换成本低 |
| 应用标识 | `com.bowknot.agent` [待确认] | mygo 的 identifier 用于权限、偏好与注册 |
| 应用名与版本 | `Bowknot Agent`，`0.1.0` | 与后续 `v0.1.0` tag 对齐 |
| 入口布局 | 根 `main.go` 薄入口，仅装配 `internal/app` | 适配 mygo 原生模板；业务代码不落在根目录 |
| 分层 | `main → internal/app → internal/<模块>`，模块间通过接口/事件通信，禁止 UI 反向依赖 | 对应“渲染界面不得直接访问密钥”等约束 |
| 平台差异 | 统一收敛到 `internal/platform` | macOS / Windows 托盘、快捷键、自启动差异集中处理 |
| WebApp 能力 | 统一收敛到 `internal/capability` | 所有 WebApp 同一能力集，宿主注入 |
| 不引入 `pkg/` | 全部使用 `internal/` | 当前无对外复用需求，避免过早公开 API |
| 不引入 Makefile | 命令直接写进 AGENTS.md，统一用 `go` / `go tool mygo` | Windows 默认无 make，双平台一致性优先 |
| 不引入 golangci-lint | 本机未安装；先用 `gofmt` + `go vet` + `go test` | 避免为 001 增加工具安装成本，后续再评估 |
| 原型代码 | 不进入本仓库 | requirements.md 8.3 明确原型不作为实现基础 |

## 4. 基础目录结构

```text
bowknot-agent/
├── AGENTS.md                     # 开发规范与协作约定（唯一入口）
├── go.mod                        # module + Go 1.27 + mygo tool
├── mygo.json                     # 应用名、identifier、版本、构建输出
├── main.go                       # 薄入口：装配 app 并 Run
├── resources/
│   └── icon.png                  # 1024×1024 占位图标
├── internal/
│   ├── app/                      # 生命周期、主窗口装配、单实例
│   ├── shell/                    # 主界面导航与模块容器（FR-SHELL）
│   ├── platform/                 # macOS / Windows 差异封装（托盘、快捷键、自启动、路径）
│   ├── store/                    # 应用商店与 WebApp 容器（FR-APP）
│   ├── capability/               # 统一能力集实现与注入（FR-APP-06）
│   ├── agent/                    # 主 Agent / 子 Agent（FR-AGT）
│   ├── pet/                      # 桌宠窗口与交互（FR-PET）
│   ├── memory/                   # 长期记忆（FR-MEM）
│   ├── creation/                 # 图片 / 视频 / 语音（FR-CRE）
│   ├── gateway/                  # 本地 AI 网关（FR-GW）
│   ├── task/                     # 任务中心（FR-TASK）
│   ├── license/                  # 授权与订阅（FR-LIC）
│   ├── privacy/                  # 审计与数据管理（FR-PRIV）
│   ├── ui/                       # 通用布局、组件与主题
│   └── settings/                 # 设置界面装配
└── docs/
    ├── requirements.md           # 已有需求文档，不移动
    └── tasks/
        └── 001-task.md
```

目录落地规则：

- 空目录放 `.gitkeep`，保证目录进入版本库；不创建无法编译的 Go 包。
- 001 只创建 `internal/app`、`internal/platform`、`internal/ui` 的可编译包与 `main.go` 对应代码，其余目录先占位。
- 不创建 `cmd/`、`frontend/`、`pkg/`、`scripts/`、`testdata/`。

## 5. 交付物与验收

| 编号 | 交付物 | 要求 | 验收方式 |
| --- | --- | --- | --- |
| D1 | `go.mod` | module 路径、`go 1.27`、mygo CLI 以 tool 固定 | `go tool mygo --version` 输出版本 |
| D2 | `mygo.json` | name / identifier / version / out | `go tool mygo doctor` 无配置错误 |
| D3 | `main.go` | 创建并显示主窗口，标题 `Bowknot Agent`，内容为占位文案 | `go tool mygo dev` 出现窗口 |
| D4 | `resources/icon.png` | 1024×1024 PNG 占位 | 构建产物带图标，无缺失告警 |
| D5 | 目录骨架 | 与第 4 节一致，含 `.gitkeep` 说明文件 | 目录清单比对 |
| D6 | `AGENTS.md` | 覆盖第 6 节大纲全部条目，命令与目录属实 | 逐条核对，命令可执行 |
| D7 | `.gitignore` | 忽略 `build/`、`.mygo/`、`dist/`、二进制、IDE 与本机配置 | `git status` 无构建产物 |
| D8 | `.gitattributes` | `* text=auto eol=lf`，避免双平台换行差异 | 文件存在且内容正确 |
| D9 | `.editorconfig` | UTF-8、LF、Go 用 tab、JSON/YAML/Markdown 用 2 空格 | 文件存在且内容正确 |
| D10 | 本计划 | 状态随实施更新 | 文档存在且状态准确 |

## 6. AGENTS.md 内容大纲

1. **项目概览**：本地优先桌面 AI 工作台；四个组成部分；指向 `docs/requirements.md`，不在此重复需求。
2. **已确认约束**：不使用 Electron；Go 1.27+；原生 UI 用 mygo `ui`；WebApp 用系统 webview；目标 macOS + Windows；不使用 cgo。
3. **目录职责与依赖方向**：第 4 节表格化；明确 `main → app → 模块`，禁止模块直接互相写入对方状态。
4. **常用命令**：`go tool mygo dev`、`go tool mygo build`、`go test ./...`、`go vet ./...`、`gofmt -l .`、`go tool mygo doctor`、`go get -tool ...@v0.6.4`。
5. **编码规范**：命名、包边界、错误包装 `%w`、`context` 传递、避免全局可变状态、UI 视图纯展示、禁止在视图函数内做阻塞 IO。
6. **测试规范**：`_test.go` 与被测包同目录；表驱动；视图用 mygo 的无窗口测试；外部依赖用接口替身；新增逻辑必须带测试。
7. **提交规范**：Conventional Commits（`feat` / `fix` / `docs` / `refactor` / `test` / `build` / `chore`），单次提交单一目的，提交信息中文正文。
8. **分支规范**：主分支 `master`；任务分支 `NNN-slug`；完成后合回。
9. **文档规范**：任务计划放 `docs/tasks/NNN-task.md`，编号递增、状态字段（草稿 / 实施中 / 完成）；需求变更必须回写 `requirements.md`；`AGENTS.md` 是规范唯一入口，不另建规范文档。
10. **安全与隐私**：密钥只允许存在网关侧且不入库；日志不记录密钥与完整对话正文（对齐 FR-GW-03、6.4）；能力访问必须走 `internal/capability`。
11. **完成定义（DoD）**：编译通过、`go vet` 与 `go test` 通过、计划文档状态更新、无构建产物与密钥入库。

## 7. 基础开发规范要点

| 类别 | 规则 |
| --- | --- |
| 格式化 | `gofmt` 强制；import 分组标准库 / 第三方 / 本项目；不手工对齐 |
| 包命名 | 小写单词、不加下划线、避免与类型重名；目录名即包名 |
| 依赖方向 | `internal/app` 可依赖任意模块；模块之间只通过显式接口或事件；`ui` 不得依赖业务模块 |
| 错误 | 返回 `error`，用 `%w` 包装并保留上下文；不用 `panic` 表达业务失败；错误分类沿用 requirements.md 6.3 |
| 并发 | 长任务放 goroutine；跨 goroutine 更新 UI 状态必须走 mygo 提供的线程安全方式；所有跨层调用传 `context.Context` |
| 日志 | 本地结构化日志；字段化而非拼接；禁止密钥、令牌、完整用户正文 |
| 配置与密钥 | 密钥不进仓库、不进界面进程；本机配置放系统用户目录，不放仓库 |
| 测试 | 单元测试同包；覆盖率不设硬门槛，但新逻辑必须有测试；集成测试等真实能力接入后再补 |
| 版本 | `mygo.json` version 与 git tag `vX.Y.Z` 同步；构建号不在仓库内手改 |
| 生成物 | `build/`、`.mygo/` 不入库；若后续引入 `mygo generate` 产物，需在 AGENTS.md 明确是否入库 |

## 8. 实施步骤

| 步骤 | 内容 | 完成标志 |
| --- | --- | --- |
| S1 | 初始化 `go.mod`，并以 tool 固定 mygo CLI（本机固定 `v0.5.5`） | `go tool mygo --version` 输出 0.5.5 |
| S2 | 写 `mygo.json` 与占位图标 | `go tool mygo doctor` 通过 |
| S3 | 写根 `main.go` 与 `internal/app` 最小装配 | `go tool mygo dev` 打开主窗口 |
| S4 | 创建第 4 节目录骨架（`.gitkeep`） | 目录清单一致 |
| S5 | 写 `AGENTS.md` | 第 6 节条目齐全且命令可执行 |
| S6 | 写 `.gitignore`、`.gitattributes`、`.editorconfig` | 文件生效，`git status` 干净 |
| S7 | 自检：`gofmt -l .`、`go vet ./...`、`go test ./...`、`go tool mygo build` | 全部通过，产物在 `build/windows-amd64/` |
| S8 | 更新本计划状态；提交（可选，需用户确认） | 计划状态为“完成”，提交内容符合规范 |

## 9. 整体验收标准

- `go tool mygo dev` 在 Windows 启动主窗口，无崩溃、无控制台报错。
- `gofmt -l .` 无输出；`go vet ./...` 与 `go test ./...` 通过。
- `go tool mygo build` 生成 `build/windows-amd64/Bowknot Agent.exe`。
- 目录结构与 `AGENTS.md` 描述一致，无多余空目录、无未约定顶层目录。
- 仓库内无密钥、无 `build/`、`.mygo/` 等生成物。
- macOS 构建与运行验证不在 001 内完成，但需在计划风险中保留并交由后续计划。

## 10. 风险与待确认

| 编号 | 事项 | 影响 | 当前处理 |
| --- | --- | --- | --- |
| R-01 | Module 路径未定 | 影响所有 import 路径 | 先用 `github.com/bowknot/bowknot-agent`，确定远端后统一替换 |
| R-02 | identifier 未定 | 影响权限、偏好、安装注册 | 先用 `com.bowknot.agent` |
| R-03 | macOS 环境不可用 | 无法在 001 验证 macOS 构建、公证、托盘行为 | Windows 先行；macOS 验证列入后续计划 |
| R-04 | 占位图标来源 | 构建缺图标会告警 | 使用自生成占位 PNG，后续替换正式图标 |
| R-05 | mygo 对 8.2 能力支持未知 | 可能影响目录分层 | 001 只建目录，不预设实现；验证结果回写 requirements.md Q-01 |
| R-06 | 是否 001 即引入 golangci-lint 与 CI | 影响工具链与提交门禁 | 暂不引入，按需在后续计划评估 |
| R-07 | 是否创建 README / LICENSE | 影响仓库对外呈现 | 暂不创建，待确认 |

## 11. 后续计划候选

- 002：主界面框架（左导航 / 顶部状态 / 模块容器，FR-SHELL-01）与模块状态保留。
- 003：托盘、单实例、窗口状态恢复（FR-SHELL-02/03）。
- 004：mygo 能力验证清单执行（8.2），产出可行性结论并回写 Q-01。
- 005：AI 网关最小闭环（供应商配置 + 连接测试，FR-GW-01/03）。

## 12. 实施记录（2026-10-26）

| 项 | 结果 |
| --- | --- |
| 交付物 | D1–D10 全部落地；目录结构与第 4 节一致，`internal/` 下 16 个模块目录就位 |
| 入口 | 根 `main.go` 仅调用 `internal/app`；`internal/app` 装配主窗口；占位首页在 `internal/ui` |
| mygo 版本 | `v0.5.5`。本机无外网，模块缓存中最高版本为 0.5.5；网络恢复后执行 `go get -tool github.com/egoist/mygo/cmd/mygo@v0.6.4` 并重跑自检 |
| `gofmt -l .` | 无输出 |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过（`internal/ui` 无窗口视图测试） |
| `go tool mygo build` | 通过，产物 `build/windows-amd64/Bowknot Agent.exe` 与 `Bowknot Agent Setup 0.1.0 amd64.exe` |
| `go tool mygo dev` | 通过，开发窗口标题 `Bowknot Agent` |
| 生成物入库 | `build/`、`.mygo/` 已由 `.gitignore` 忽略 |
