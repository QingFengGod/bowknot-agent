# 002 主窗口与骨架 UI

| 项 | 内容 |
| --- | --- |
| 计划编号 | 002 |
| 状态 | 实施中（功能完成，界面细节待人工验收） |
| 更新日期 | 2026-10-29 |
| 关联需求 | FR-SHELL-01 主界面结构、FR-SHELL-02 窗口尺寸与位置记忆 |
| 前置计划 | 001 项目搭建（已完成） |

## 1. 简介

实现主窗口壳与骨架界面：窗口占工作区 80% 居中、最小 960×640、记忆位置与尺寸；自绘标题栏（logo + 应用名 + 主题按钮 + 三色交通灯，可拖拽、双击最大化）；亮 / 暗两套主题令牌与本地持久化；左侧四模块导航与右侧占位内容。不实现任何模块业务内容、托盘与全局状态。

## 2. 任务拆分

| 编号 | 小任务 | 状态 |
| --- | --- | --- |
| T1 | 校正 `AGENTS.md`：mygo 版本与 `go.mod` 对齐，补充 `{UserHomeDir}/.bowknot-agent/` 数据根目录约定 | 完成 |
| T2 | `internal/ui/theme.go`：亮 / 暗令牌、橙色强调、`Theme(mode)` 与单测 | 完成 |
| T3 | `internal/ui/titlebar.go`：隐藏系统标题栏与按钮、拖拽 / 双击、三色灯、主题切换按钮、可访问性 `Label` | 完成 |
| T4 | `internal/app/app.go`：工作区 80%、居中、最小尺寸、`StateKey`、初始白底（`windowSize` 纯函数 + 单测） | 完成 |
| T5 | `internal/shell`：`ButtonBase` 四模块导航（选中常亮橙）、占位内容、默认 `store` | 完成 |
| T6 | `internal/platform` 路径函数（`DataDir` / `SettingsPath`）+ `internal/app/settings.go` 主题持久化与容错 | 完成 |
| T7 | 删除 `internal/ui/home.go` 与旧测试，由 shell 接管首页 | 完成 |
| T8 | 自检（`gofmt` / `go vet` / `go test` / `go tool mygo build`）与本计划更新 | 完成 |

## 3. 实施顺序

`T1 → T2 → T3 → T4 → T5 → T6 → T7 → T8`

主题令牌先定（T2），避免标题栏与侧栏各写一套颜色；标题栏是唯一可能推翻整窗布局的系统能力，紧随其后（T3）；窗口参数与骨架在壳稳定后一次成型（T4、T5）；持久化依赖主题与标题栏按钮，放在最后（T6）。

## 4. 实施总结

- 交付：主题、标题栏、四模块导航、窗口参数与主题持久化全部落地；`internal/ui/home.go` 已删除。
- 自检：`gofmt -l .` 无输出，`go vet ./...`、`go test ./...`、`go tool mygo build` 通过。
- 手动抽查：首启窗口居中；标题栏可拖拽 / 双击最大化；主题切换写入 `~/.bowknot-agent/settings.json` 并重启保持。
- 偏差：因 `ui.Sidebar` 失焦会置灰、无法满足「常亮橙色」，改为 `ButtonBase` 自定义导航。
- 遗留：视觉间距、色彩与拖拽手感待人工验收；默认主题自 003 起改为「跟随系统」。
