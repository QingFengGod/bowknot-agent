# 003 桌面壳 P0 收尾计划

| 项 | 内容 |
| --- | --- |
| 计划编号 | 003 |
| 状态 | 实施中 |
| 更新日期 | 2026-10-27 |
| 关联需求 | FR-SHELL-03 托盘与退出、FR-SHELL-02 单实例与窗口状态、FR-SHELL-01 模块状态保留 |
| 前置计划 | 002 主窗口与骨架 UI（实施中，功能与 UI 待人工验收） |
| 目标版本 | v0.1.0-dev |
| 范围 | 托盘、单实例、关闭到托盘、三态主题、窗口失焦态、macOS 全屏语义 |

## 1. 目标

### 1.1 本次交付

1. 单实例：重复启动时把命令行交给已运行实例，打开并聚焦主窗口，新实例立即退出。
2. 托盘：托盘图标 + 菜单（打开主界面、退出），左键单击托盘图标唤起主窗口，右键弹出菜单。
3. 关闭到托盘：标题栏关闭按钮与系统关闭一致，关闭主窗口只隐藏、不退出进程；从托盘退出才结束进程。
4. 关闭到托盘的回退：托盘创建失败时不拦截关闭，关闭最后一个窗口即退出，避免留下无托盘的僵尸进程。
5. 三态主题：亮 → 暗 → 跟随系统循环，默认跟随系统；跟随系统时随系统外观变化实时更新窗口底色与界面。
6. 窗口失焦态：主窗口失焦时交通灯置灰，重新聚焦恢复彩色。
7. green 按钮平台语义：macOS 触发全屏切换，Windows 保持最大化 / 还原；差异收敛到 `internal/platform`。
8. 标题栏三态主题图标与提示、可访问性标签；设置文件支持 `system` 值。
9. 单元测试、自检与本计划状态更新。

### 1.2 非目标

- 不做顶部全局状态条（网关状态、当前模型、用量入口）。
- 不做托盘「显示桌宠」项（桌宠模块尚未实现）。
- 不做有运行中任务时的退出确认（任务中心尚未实现）。
- 不做各模块内容状态保留；模块仍是占位内容，切换不重建窗口。
- 不做通知、全局唤起、命令面板、自启动。
- 不做 macOS 实机验证（本机为 Windows），按 R-04 交由用户在 Mac 上验收。
- 不做关闭到托盘的首次提示、动画或 Dock 图标隐藏。

### 1.3 已确认决策（2026-10-27）

| 编号 | 决策 | 落点 |
| --- | --- | --- |
| C-01 | 关闭主窗口默认**隐藏到托盘**，不退出进程；requirements FR-SHELL-03 去掉对应 `[Assumption, TBD]` | 1.1、3.3、requirements.md、D3 |
| C-02 | 主题改为**三态循环**：亮 → 暗 → 跟随系统，默认「跟随系统」，覆盖 002 的默认亮色与回退亮色 | 3.4、3.7、D6、D7 |
| C-03 | 托盘创建失败时**不注册关闭拦截**，关闭最后一个窗口即退出；不保留无托盘进程 | 3.3、R-02、D3 |
| C-04 | macOS green 按钮触发**全屏切换**，Windows 保持最大化 / 还原；平台判断放 `internal/platform` | 3.6、D5 |
| C-05 | 托盘图标使用 `resources` 包内嵌的 32×32 黑 + 透明 PNG，`IconIsTemplate: true` 交给系统着色 | 3.2、D4 |

## 2. 前置状态与 mygo API 核实

002 已交付：主窗口（工作区 80%、最小 960×640、`StateKey: "main"`）、自绘标题栏与交通灯、亮 / 暗两套令牌、主题持久化、四模块占位导航。

| 能力 | mygo 0.6.4 结论 | 影响 |
| --- | --- | --- |
| 单实例 | `App.RequestSingleInstanceLock() bool`，第二个实例返回 false 并转发参数；`App.OnSecondInstance(func(args []string, wd string))` | 首实例注册回调唤起窗口；未取得锁的实例直接返回 |
| 关闭事件 | `Window.OnClose(func(e *CloseEvent))`，`e.PreventDefault()` 阻止关闭；`App.Quit()` 也会触发窗口 `OnClose` | 需要 `quitting` 标记区分「用户关闭」与「真正退出」 |
| 全部窗口关闭 | `App.OnWindowAllClosed(func())` 注册后最后一个窗口关闭也不退出 | 仅托盘创建成功时注册 |
| 退出前 | `App.OnBeforeQuit(func(e *QuitEvent))` 在关闭窗口之前调用 | 在 `quitting` 前略设为 true，让关闭不被拦截 |
| 激活 | `App.OnActivate(func(hasVisibleWindows bool))`；`App.Show()` / `App.IsHidden()` | macOS Dock 点击在无可见窗口时唤起 |
| 托盘 | `mygo.NewTray(TrayOptions{ID, Icon []byte, IconIsTemplate, Title, ToolTip, Menu})`；`Tray.SetMenu / Destroy / OnClick`；必须在 `WhenReady` 之后创建 | ID 固定让 Windows 记住任务栏 / 溢出区位置 |
| 托盘菜单 | `mygo.NewMenu([]*mygo.MenuItem{{ID, Label, Click}})` + `mygo.Separator()`；`Click func(item *MenuItem, win *Window)` | 打开主界面、退出两项 |
| 主题源 | `mygo.Theme.SetSource(mygo.ThemeSystem / ThemeLight / ThemeDark)` 可在 `App.Run` 前调用；`IsDark()` 取有效外观；`OnUpdated(func())` 监听系统外观变化 | 三态主题与跟随系统 |
| 窗口 | `Hide / Show / Focus / Invalidate / SetBackgroundColor / OnFocus / OnBlur / ToggleFullScreen / IsFullScreen / IsMaximized` | 隐藏到托盘、失焦态、全屏 |
| 重绘 | 外部状态变化后必须 `Window.Invalidate()` 才重绘 | 焦点与主题变化后调用 |
| 资源嵌入 | `resources/` 下新建 `package resources`，`//go:embed tray.png`，导出 `TrayIcon []byte` | 托盘图标随包发布，不依赖运行时文件 |

关键结论：`App.Quit()` 会触发每个窗口的 `OnClose`，因此关闭拦截必须由 `OnBeforeQuit` 设置的 `quitting` 标记放行，否则托盘「退出」会被自己的拦截逻辑取消。

## 3. 设计规格

### 3.1 单实例

| 项 | 规格 |
| --- | --- |
| 取得锁 | `Run` 最开始调用 `mygo.App.RequestSingleInstanceLock()`；返回 false 时 `Run` 返回 nil，进程静默退出 |
| 唤起 | 首实例 `OnSecondInstance` 中调用 `showMainWindow`：`App.Show()`、`Window.Show()`、`Window.Focus()` |
| 注册时机 | `OnSecondInstance`、`OnBeforeQuit`、`OnActivate` 在 `App.Run()` 之前注册，保证启动早期的第二实例也能被接住 |
| macOS | `OnActivate(hasVisibleWindows=false)` 时同样唤起主窗口 |

### 3.2 托盘

| 项 | 规格 |
| --- | --- |
| 图标 | `resources/tray.png`，32×32，黑色墨迹 + 透明底，蝴蝶结造型；`IconIsTemplate: true`，Windows 暗色任务栏自动显示白色 |
| 选项 | `ID: "main"`、`ToolTip: "Bowknot Agent"`、菜单见下 |
| 菜单 | 「打开主界面」→ `showMainWindow`；分隔线；「退出」→ `App.Quit()` |
| 左键 | Windows 下调用 `Tray.OnClick` 唤起主窗口；右键弹出菜单 | 菜单仍在右键，退出入口不丢 |
| macOS | 有菜单时左 / 右键都由系统弹出菜单，点击不唤起窗口 | 系统约定，记入 R-04；不为此去掉菜单 |
| 创建时机 | `App.WhenReady` 内创建；失败仅记录日志并标记 `hasTray=false` |
| 生命周期 | 托盘引用保存在 `App`，进程运行期间不销毁 |

### 3.3 关闭到托盘与退出

| 场景 | 行为 |
| --- | --- |
| 用户点标题栏关闭 | `Window.OnClose`：若 `quitting` 为 false，`PreventDefault()` + `Window.Hide()` |
| 系统关闭（Alt+F4 / Cmd+W） | 与标题栏关闭一致，隐藏到托盘 |
| 托盘「退出」 | `App.Quit()` → `OnBeforeQuit` 置 `quitting=true` → `OnClose` 放行 → 进程结束 |
| 托盘创建失败 | 不注册 `OnClose` 拦截与 `OnWindowAllClosed`，关闭即退出 |
| 全部窗口关闭 | 托盘可用时注册 `OnWindowAllClosed` 空回调，保证进程继续驻留 |

### 3.4 主题三态

| 项 | 规格 |
| --- | --- |
| 偏好 | `ui.Preference` 取值 `light` / `dark` / `system` |
| 循环 | `light → dark → system → light`（`Preference.Next()`） |
| 默认 | `system`；设置缺失、损坏、值未知一律回退 `system`（覆盖 002 的 `light`） |
| 启动 | `App.Run` 前 `Theme.SetSource(pref.Source())`；`WhenReady` 后按 `Theme.IsDark()` 解析有效外观 |
| 切换 | 点击主题按钮：更新偏好 → `SetSource` → 按 `IsDark()` 解析 → 更新 `shell.Preference` / `shell.Theme` → `SetBackgroundColor` + `Invalidate` → 写入设置 |
| 跟随系统 | 注册 `Theme.OnUpdated`，当偏好为 `system` 时重新解析并重绘 |
| 标题栏图标 | `light` 显示月亮（提示「切换到暗色主题」）、`dark` 显示太阳（提示「切换到跟随系统主题」）、`system` 显示自动图标（提示「切换到亮色主题」）；`Label` 保持「切换主题」 |
| 有效外观 | `ui.Resolve(pref, systemDark)` 纯函数返回 `ui.Mode`；`light`/`dark` 忽略系统值 |

### 3.5 失焦态

| 项 | 规格 |
| --- | --- |
| 状态 | `shell.Focused`，默认 true；`Window.OnFocus / OnBlur` 写入并 `Invalidate()` |
| 交通灯 | 失焦时三色灯统一使用 `theme.Border`，聚焦时恢复 `#ff5f57 / #febc2e / #28c840` |
| 其他 | 导航选中态保持橙色（002 C-02 不变）；标题栏与内容不因失焦重建 |

### 3.6 green 按钮平台分支

| 平台 | 行为 |
| --- | --- |
| macOS | `platform.UsesFullScreenZoom()` 为 true；点击 green 调用 `Window.ToggleFullScreen()` |
| Windows | 返回 false；点击 green 保持 `IsMaximized() ? Unmaximize() : Maximize()` |

macOS 全屏后自绘标题栏随内容保留，属已知限制，记入 R-04。

### 3.7 设置文件

| 项 | 规格 |
| --- | --- |
| 格式 | `{"theme":"light"}` / `{"theme":"dark"}` / `{"theme":"system"}` |
| 默认 | 文件缺失、JSON 非法、值为未知时返回 `system`，记录日志，不阻断启动 |
| 写入 | 每次主题按钮循环后写入；失败不回滚当前主题 |
| 兼容 | 002 写入的 `light` / `dark` 继续有效 |

## 4. 优先级评估与实施顺序

结论：**先主题三态（纯逻辑、单测便宜），再失焦态与平台分支（标题栏改动小），接着托盘与关闭到托盘（唯一可能影响进程退出的系统行为），最后单实例与系统主题联动**。

| 优先级 | 内容 | 理由 |
| --- | --- | --- |
| P0 | 主题三态纯逻辑 + 设置读写 | 无窗口依赖，表驱动测试可一次盖住；决定标题栏图标与启动流程 |
| P0 | 失焦态与 green 平台分支 | 改动集中在标题栏与 `platform`，风险低 |
| P0 | 托盘、关闭到托盘、退出放行 | 决定进程能否正常退出，必须实机验证；失败回退路径要一起写 |
| P1 | 单实例与 `OnActivate` | 依赖托盘与唤起入口；Windows 可自动化验证的部分有限 |
| P1 | 测试、自检、文档 | 满足 DoD |

不建议先做单实例：它依赖 `showMainWindow`，而该函数随托盘一起成型，先写会返工。

## 5. 交付物与验收

| 编号 | 交付物 | 要求 | 验收方式 |
| --- | --- | --- | --- |
| D1 | `internal/ui/theme.go` | `Preference` 三值与 `Next`、`Resolve`、`Source`；`Mode` 保持亮 / 暗 | 表驱动单测覆盖循环与解析 |
| D2 | `internal/ui/titlebar.go` | 三态图标与提示；失焦灰灯；`Label` 保持 | `ui.NewTester` 视图测试 + 人工视觉验收 |
| D3 | `internal/app/app.go` | 单实例、托盘、关闭隐藏、退出放行、`OnActivate`、焦点重绘、主题切换与系统联动 | 纯函数单测 + 人工验证托盘 / 关闭 / 单实例 |
| D4 | `resources/tray.png` + `resources/resources.go` | 32×32 模板 PNG，`//go:embed` 导出 `TrayIcon` | 编译通过；托盘图标可见 |
| D5 | `internal/platform/zoom_*.go` | `UsesFullScreenZoom()`：darwin true，其余 false | 当前平台单测 + macOS 人工验证 |
| D6 | `internal/app/settings.go` | `theme` 支持三值，默认 `system` | 表驱动单测覆盖六种输入 |
| D7 | `internal/shell/shell.go` | `Preference`、`Focused`、`Theme` 有效外观 | 视图测试断言标题栏三态与灰灯 |
| D8 | `docs/requirements.md` | FR-SHELL-03 确认关闭到托盘 | 文档核对 |
| D9 | 002 / 001 计划校正 | 002 §3.5 注明默认值变更；001 §11、002 §9 候选编号与 003 对齐 | 文档核对 |
| D10 | 本计划 | 状态、决策与实施记录更新 | 文档状态准确 |

## 6. 实施步骤

| 步骤 | 内容 | 完成标志 |
| --- | --- | --- |
| S0 | 写本计划；更新 requirements FR-SHELL-03；校正 001 §11、002 §9 与 002 §3.5 | 文档一致，无编号冲突 |
| S1 | `ui.Preference` + `Next` + `Resolve` + `Source` + 单测；标题栏三态图标、提示与失焦灰灯 | 单测通过；`ui.NewTester` 可断言三态 |
| S2 | 生成 `resources/tray.png`，新增 `resources/resources.go` | `go build ./...` 通过 |
| S3 | `platform.UsesFullScreenZoom()` 双平台文件 + 测试 | 当前平台测试通过 |
| S4 | `shell` 增加 `Preference`、`Focused`，标题栏接入 | 视图测试通过 |
| S5 | `app`：`RequestSingleInstanceLock`、`OnSecondInstance`、`OnActivate`、`OnBeforeQuit`、托盘与菜单、`OnClose` 隐藏、焦点重绘 | 实机：关闭后进程驻留，托盘可唤起 |
| S6 | `app`：三态主题、系统外观联动、设置读写迁移到 `system` | 重启保持偏好；系统切换即时生效 |
| S7 | 单测：settings 六种输入、ui 循环 / 解析、app 关闭决策 | `go test ./...` 通过 |
| S8 | 自检 `gofmt -l .`、`go vet ./...`、`go test ./...`、`go tool mygo build`；更新实施记录与人工验收步骤 | 全部通过 |

## 7. 验收标准

- 关闭主窗口后窗口消失、进程仍在，托盘图标存在；托盘「打开主界面」可唤起并聚焦窗口。
- 托盘「退出」后进程结束，无残留；重复点击退出无异常。
- 重复启动应用时只有一个主窗口，第二次启动退出并唤起第一次的窗口。
- 主题按钮按 亮 → 暗 → 跟随系统 循环；标题栏图标与提示随状态变化；重启保持偏好。
- 偏好为「跟随系统」时，修改系统亮 / 暗外观，应用界面即时跟随。
- 主窗口失焦时交通灯置灰，聚焦后恢复彩色；导航选中态仍为橙色。
- Windows 下 green 为最大化 / 还原；macOS 下（人工验证）为全屏切换。
- `settings.json` 缺失、损坏或值未知时回退「跟随系统」且不崩溃。
- `gofmt -l .` 无输出，`go vet ./...`、`go test ./...` 通过，`go tool mygo build` 成功。

## 8. 风险与待确认

| 编号 | 事项 | 影响 | 当前处理 |
| --- | --- | --- | --- |
| R-01 | `App.Quit()` 触发 `OnClose`，可能被关闭拦截取消 | 托盘退出失效、进程无法结束 | `OnBeforeQuit` 置 `quitting` 标记放行；单测覆盖关闭决策纯函数；S5 实机验证 |
| R-02 | 托盘创建失败（系统不支持 / 权限） | 无托盘时关闭窗口会导致进程驻留或被判僵尸 | 失败即不拦截关闭、不注册 `OnWindowAllClosed`，回退为关闭即退出 |
| R-03 | Windows 任务栏溢出区与图标着色 | 图标可能不可见 | 使用 `IconIsTemplate` 模板图标，`ID` 固定记忆位置；S5 验证 |
| R-04 | macOS 全屏后自绘标题栏与系统全屏空间；有菜单的托盘左键只弹菜单、不唤起窗口 | 标题栏可能压住内容或随全屏消失；macOS 无法用左键唤起 | 接受为已知限制，macOS 实机验证时记录；不为此去掉退出菜单 |
| R-05 | `Theme.OnUpdated` 在偏好为亮 / 暗时也会触发 | 可能覆盖用户选择 | 回调内仅在偏好为 `system` 时重新解析 |
| R-06 | `OnFocus / OnBlur` 在 Windows 最小化或托盘操作时的触发差异 | 灰灯状态可能与实际不符 | `Invalidate` 重绘；S5 实机观察，必要时补 `OnRestore` |
| R-07 | macOS 本机不可验证 | 托盘、全屏、Dock 行为未知 | Windows 先验收，macOS 列入人工验证清单 |
| R-08 | 003 覆盖 002 的默认主题 | 与 002 记录不一致 | 002 §3.5 注明自 003 起默认「跟随系统」 |

## 9. 后续计划候选

- 004：左侧导航栏底部全局状态、模块状态保留、App Store 列表与详情，以及已安装应用启动器与本地注册表（FR-SHELL-01、FR-APP-01、FR-APP-11）。
- 005：App Store 安装 / 卸载 / 启动与 webview 容器（FR-APP-02、FR-APP-03）。
- 006：网关最小闭环（供应商配置、连接测试、状态快照与用量记录），把状态位接真实数据（FR-GW）。
- 007：mygo 能力验证清单执行（requirements 8.2），产出可行性结论并回写 Q-01。

## 10. 实施记录（2026-10-27）

| 项 | 结果 |
| --- | --- |
| S0 | 本计划建立；requirements FR-SHELL-03 去掉关闭到托盘的 `[Assumption, TBD]`；001 §11、002 §9 候选编号对齐，002 §3.5 注明默认值变更 |
| S1 | `internal/ui`：`Preference` 三值、`Next`、`Resolve`、`Source`、`AutoIcon`；标题栏三态图标与失焦灰灯；单测覆盖 |
| S2 | `resources/tray.png`（32×32 模板图标）+ `resources/resources.go`（`//go:embed`） |
| S3 | `internal/platform`：`UsesFullScreenZoom()`（darwin true，其余 false） |
| S4 | `internal/shell`：`Preference`、`Focused` 接入标题栏；视图测试覆盖 |
| S5 | `internal/app`：单实例、`OnSecondInstance`、`OnActivate`、`OnBeforeQuit`、托盘菜单、左键唤起、`OnClose` 隐藏、焦点重绘；关闭决策纯函数单测 |
| S6 | `internal/app/settings.go`：`theme` 支持 `system`，默认与回退 `system`；表驱动单测覆盖 |
| S7 | 单测：settings 六种输入、ui 循环与解析、app 关闭决策、shell 三态与失焦 |
| S8 | 自检结果见 §11；代码与自动化验收完成，托盘 / 单实例 / 关闭 / 系统主题 / 失焦待用户人工验证 |

## 11. 自检结果（2026-10-27）

| 命令 | 结果 |
| --- | --- |
| `gofmt -l .` | 无输出 |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过（app / platform / shell / ui 四个包） |
| `go tool mygo build` | 通过，产物 `build/windows-amd64/Bowknot Agent.exe` |

## 12. 人工验收步骤（Windows）

入口：`go tool mygo dev`，或运行 `build/windows-amd64/Bowknot Agent.exe`（安装包为同目录 Setup）。

1. 关闭到托盘：点标题栏关闭按钮（或 Alt+F4）。预期窗口消失、进程仍在、通知区域出现蝴蝶结图标；再次运行 exe 不应出现第二个窗口，而是原窗口重新显示并聚焦。
2. 托盘唤回：左键单击托盘图标，窗口应显示并聚焦；右键托盘图标，点「打开主界面」也应显示并聚焦，当前模块与主题保持不变。
3. 托盘退出：右键托盘图标，点「退出」。预期进程结束、图标消失；任务管理器中无残留进程。
4. 三态主题：从任一起点连点标题栏主题按钮，顺序为「亮 → 暗 → 跟随系统」循环；每步图标与提示同步变化，界面底色无闪白。重启后保持上次偏好。
5. 跟随系统：把偏好停在「跟随系统」，在 Windows 设置中切换系统亮 / 暗外观。预期应用界面即时跟随；停在「亮」或「暗」时切换系统外观不应改变应用。
6. 失焦态：点击其他窗口使主窗口失焦。预期三个交通灯变灰；点回主窗口恢复红黄绿；左侧导航选中项始终为橙色。
7. 最大化：点绿按钮在最大化 / 还原间切换，无布局错位或标题栏闪烁。
8. 设置容错：从托盘退出应用，把 `C:/Users/<用户>/.bowknot-agent/settings.json` 改为非法 JSON 或删除后启动。预期不崩溃，主题回退「跟随系统」。

重点观察：托盘图标在任务栏溢出区是否可见、关闭后进程是否驻留、退出是否彻底、主题切换时是否闪白、失焦灰灯是否与真实焦点一致。

macOS 相关（托盘模板图标、Dock 点击唤起、green 全屏）按 R-04 / R-07 留待 Mac 实机验证。
