# 001 项目搭建

| 项 | 内容 |
| --- | --- |
| 计划编号 | 001 |
| 状态 | 完成 |
| 更新日期 | 2026-10-29 |
| 关联需求 | requirements.md 8.1 已确认约束、8.2 mygo 能力验证项、FR-SHELL-01 |
| 前置计划 | — |

## 1. 简介

建立可编译、可启动的 mygo 原生 UI 工程骨架：薄入口主窗口、按需求模块划分的目录、`AGENTS.md` 开发规范与基础工具链。只建骨架，不实现任何业务功能（商店、Agent、创作、网关、授权均只占位）。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | `go.mod`：module 路径、Go 1.27、mygo CLI 以 `tool` 固定版本 | 完成 |
| T2 | `mygo.json`（name / identifier / version / out）与 `resources/icon.png` 占位图标 | 完成 |
| T3 | 根 `main.go` 薄入口 + `internal/app` 最小装配，打开占位主窗口 | 完成 |
| T4 | 按模块创建 `internal/` 目录骨架，空目录用 `.gitkeep` 占位 | 完成 |
| T5 | `AGENTS.md`：约束、目录职责、命令、编码 / 测试 / 提交 / 分支 / 文档规范、DoD | 完成 |
| T6 | `.gitignore`、`.gitattributes`、`.editorconfig` | 完成 |
| T7 | 自检与构建：`gofmt`、`go vet`、`go test`、`go tool mygo build` | 完成 |

## 3. 实施顺序

`T1 → T2 → T3 → T4 → T5 → T6 → T7`

先固定模块与 mygo 版本（T1），配置与图标才能通过 `mygo doctor`（T2），再写入口与装配（T3）；目录骨架（T4）与规范（T5）在代码落地后一次成型，最后补仓库忽略规则（T6）并统一自检（T7）。

## 4. 实施总结

- 交付：工程骨架、16 个模块目录、`AGENTS.md`、三个仓库配置文件全部落地；`go tool mygo dev` 可启动标题为 `Bowknot Agent` 的主窗口。
- 自检：`gofmt -l .` 无输出，`go vet ./...`、`go test ./...` 通过，产物在 `build/windows-amd64/`。
- 偏差：本机无外网，当时 mygo 固定 `v0.5.5`；002 已升级到 `v0.6.4` 并同步 `AGENTS.md`。
- 遗留：module 路径 `github.com/bowknot/bowknot-agent` 与 identifier `com.bowknot.agent` 仍为占位；macOS 构建与签名未验证。
