# Bowknot Agent

**本地优先的桌面 AI 工作台** · Local-first desktop AI workbench

[MIT License](LICENSE) · Go 1.27+ · macOS / Windows

把第三方 Web 应用、AI 助手任务、图片 / 视频 / 语音生成收敛到同一个桌面入口：统一的窗口、统一的模型配置、统一的任务视图。所有 AI 调用经本地网关调度，数据默认留在本机。

> 状态：`v0.1.0-dev`，开发中。已完成工程底座与本地 AI 网关，Agent / 创作 / 桌宠等模块在路线图中。

---

## 为什么做

同类能力现在分散在三类地方：浏览器里的 Web 应用、各家 AI 产品的独立网页、本地的图片视频工具。切换成本高，模型配置各管各的，任务进度看不见。

Bowknot Agent 的做法是把它们放进一个桌面工作台，并且把「模型」抽象成可替换的配置项：

- 换供应商只改配置，不改产品流程
- 任务与素材留在本地，不上传云端
- WebApp 与原生模块共享同一套能力（剪贴板、文件、通知、模型调用）

---

## 核心设计

### 不用 Electron

桌面壳使用 [`mygo`](https://github.com/egoist/mygo) 的 Go 原生 UI，不引入 Chromium 与 Node 运行时。

| 选择 | 理由 |
| --- | --- |
| Go + mygo 原生 UI | 单二进制、冷启动快、内存占用低，安装包体积小一个量级 |
| 不用 cgo | 交叉编译与 CI 简单，避免平台工具链问题 |
| WebApp 窗口用系统 webview | 第三方网页只需一个容器，不需要完整浏览器运行时 |
| macOS + Windows 双平台 | 不维护 Linux，减少平台分支成本 |

### 四层职责分离

```text
main.go              薄入口，只装配 internal/app
internal/app         进程生命周期、主窗口装配
internal/shell       主界面导航与模块容器
internal/platform    macOS / Windows 差异封装（托盘、快捷键、自启动、路径）
internal/gateway     本地 AI 网关：供应商配置、模型路由、用量、密钥
internal/store       应用商店与 WebApp 容器（清单、安装、归档、容器协议）
internal/capability  统一能力集的实现与注入
internal/agent       主 Agent / 子 Agent               🚧 规划中
internal/creation    图片 / 视频 / 语音创作            🚧 规划中
internal/pet         桌宠交互入口                      🚧 规划中
internal/memory      记忆                               🚧 规划中
internal/task        任务拆解与调度                     🚧 规划中
internal/ui          主题、标题栏、图标等 UI 基础件
```

依赖方向单向向里，模块之间通过接口通信，不跨层直接引用。

---

## 已实现

**本地 AI 网关**（`internal/gateway`，14 个文件）

- 多供应商配置与模型路由：按供应商 + 模型解析调用目标
- 按能力分发：文本 / 图像 / 视频 / 语音各自独立路由
- 用量统计：按调用累计，供设置页展示
- 密钥加密存储：密钥落盘前加密，不进日志
- 统一错误适配层：把不同供应商的错误码收敛成一套产品可处理的错误

**应用商店与 WebApp 容器**（`internal/store`，17 个文件）

- 应用清单解析与校验
- 安装、卸载与归档
- 注册表持久化
- WebApp 容器通信协议

**桌面基座**（`internal/app` / `platform` / `ui`）

- 主窗口装配与生命周期
- 标题栏、主题、图标
- 缩放适配（macOS 与 Windows 分开实现）

**统一能力集**（`internal/capability`）：WebApp 与原生模块共享的剪贴板、文件、通知等能力注入。

规模：56 个 Go 文件、约 1.2 万行（含单元测试），9 个模块具备完整实现。

---

## 快速开始

要求 Go 1.27+、macOS 或 Windows。

```bash
go tool mygo doctor   # 检查本机依赖
go tool mygo dev      # 开发运行，改 .go 文件后自动重建
go tool mygo build    # 构建，产物在 build/<os>-<arch>/
```

自检：

```bash
gofmt -l .
go vet ./...
go test ./...
```

---

## 路线图

- [x] 工程骨架、架构分层与开发约定
- [x] 本地 AI 网关（供应商 / 路由 / 用量 / 密钥）
- [x] 应用商店与 WebApp 容器
- [x] 跨平台基座与原生 UI 基础件
- [ ] 主 Agent 多步任务拆解、子 Agent 协作与结果汇总
- [ ] 创作模块（图片 / 视频 / 语音）与素材库
- [ ] 桌宠交互入口与全局唤起
- [ ] 记忆与任务持久化
- [ ] 插件系统

---

## 文档

| 文件 | 内容 |
| --- | --- |
| [docs/requirements.md](docs/requirements.md) | 产品需求、优先级与开放问题 |
| [docs/tasks/](docs/tasks/) | 按编号拆解的迭代任务，含验收标准 |
| [AGENTS.md](AGENTS.md) | 工程约定、目录边界、提交与测试规范 |

---

## 关于开发方式

本项目采用**文档驱动 + AI 辅助编码**的方式开发：先写需求与任务拆分，明确模块边界与依赖方向，再让 AI 在约束内生成实现，最后由人工审查、联调和验收。

选择公开这一点，是因为它本身就是这个项目的工程方法之一——约束写清楚，AI 才能写出可维护的代码；写不清楚，生成速度只会放大返工。

---

## 许可

[MIT](LICENSE) © 2026 QingFeng

可自由使用、修改、分发与商用，保留版权声明即可。
