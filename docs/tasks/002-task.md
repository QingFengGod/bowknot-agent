# 002 主窗口与骨架 UI 计划

| 项 | 内容 |
| --- | --- |
| 计划编号 | 002 |
| 状态 | 待评审 |
| 更新日期 | 2026-10-26 |
| 关联需求 | FR-SHELL-01 主界面结构、FR-SHELL-02 窗口尺寸与位置记忆 |
| 前置计划 | 001 项目搭建（已完成） |
| 目标版本 | v0.1.0-dev |
| 范围 | 主窗口、自绘标题栏、主题系统与持久化、四模块骨架布局 |

## 1. 目标

### 1.1 本次交付

1. 主窗口：默认占主屏幕工作区 80% × 80%，居中，最小尺寸 960×640，记忆位置与大小。
2. 自绘标题栏：左侧 logo + 应用名，右侧主题切换与 macOS 风格三色交通灯，整条可拖拽、双击最大化、窗口边缘可缩放。
3. 主题系统：亮色 / 暗色两套令牌，默认亮色（白底）、主题色橙色，标题栏按钮可即时切换。
4. 主题持久化：主题偏好写入数据根目录 `{UserHomeDir}/.bowknot-agent/`，重启后恢复上次选择；文件缺失或损坏时回退亮色。
5. 骨架布局：左侧导航四个模块（App Store、助手 / Agent、创作、设置），右侧为当前模块的占位内容；选中项常亮橙色。
6. 视图测试与可访问性标签，更新本计划状态。

### 1.2 非目标

- 不实现任何模块的业务内容，右侧只放占位。
- 不实现顶部全局状态（网关状态、当前模型、用量入口，属 FR-SHELL-01 的后续部分），但标题栏右侧中间区域预留。
- 不实现托盘、通知、全局唤起、命令面板、关闭到托盘（FR-SHELL-03/04/05）。
- 不实现「各模块上次状态」的模块内恢复；002 只保证切换模块不重建窗口且保留当前选中项。
- 不做跟随系统外观，也不做窗口毛玻璃、自绘窗口圆角（见 R-06、R-07）。
- 不做窗口失焦时的交通灯置灰、macOS 全屏按钮语义（见 R-03）。

### 1.3 已确认决策（2026-10-26）

| 编号 | 决策 | 落点 |
| --- | --- | --- |
| C-01 | 主题**做持久化**：偏好写入用户数据目录，启动时读取 | 1.1 第 4 项、3.5、D8、S6 |
| C-02 | 左侧导航选中态**常亮橙色**，不随窗口失焦变灰 | 3.3、D3、S5 |
| C-03 | macOS 隐藏系统三色灯后，接受自绘 green 仅最大化 / 还原；全屏与失焦置灰列入 003 | R-03、9 |

## 2. 前置状态与 mygo API 核实

001 已交付：`go.mod`（mygo `v0.6.4`）、`mygo.json`、根 `main.go`、`internal/app`、`internal/ui`、`internal/platform`、`AGENTS.md`、目录骨架与占位首页。

| 能力 | mygo 0.6.4 结论 | 影响 |
| --- | --- | --- |
| 隐藏系统标题栏 | `WindowOptions.TitleBarStyle: mygo.TitleBarHidden` | 保留窗口阴影与边缘缩放，系统按钮浮在内容上 |
| 隐藏系统按钮 | `WindowOptions.HideWindowButtons: true` | `c.TitleBar()` 归零，视图自行绘制三色灯，仅保留窗口边缘缩放 |
| 拖拽与双击 | 元素 `.DragWindow()`；双击按标题栏行为缩放 / 最小化 | 标题栏与按钮需分层处理，避免点击被拖拽吞掉 |
| 窗口控制 | `Window.Minimize` / `Maximize` / `Unmaximize` / `Close` / `IsMaximized` | 自绘三色灯的行为实现 |
| 屏幕工作区 | `mygo.Screen.PrimaryDisplay().WorkArea`（DIPs，已排除菜单栏、Dock、任务栏） | 计算 80% 尺寸与居中 |
| 窗口记忆 | `WindowOptions.StateKey` 记忆位置、尺寸、最大化；已保存状态优先于 `Width`/`Height` | 首启 80%，之后按 FR-SHELL-02 记忆 |
| 主题 | 复制 `ui.LightTheme()` / `ui.DarkTheme()` 后 `c.SetTheme(&t)`；令牌含 `Background`、`Surface`、`Border`、`Text`、`TextMuted`、`Accent` 系列等 | 两套主题 + 橙色主题色 |
| 自绘导航 | `ui.ButtonBase(c)` 无外观按钮，配合 `Background` / `TextColor` / `Selected` / `Hovered` / `Pressed` / `Label`；`FocusGroup(ui.Vertical)` 收敛 Tab 停靠点 | 四模块导航，选中态由我们固定为橙色，不受窗口焦点影响 |
| 自绘按钮 | `ui.ButtonBase` + `Background` / `Radius` / `Size` / `Hovered` / `Label` | 三色交通灯与主题切换按钮 |
| 图标与 logo | `ui.MustParseSVG` + `ui.Icon`；`ui.DecodeBitmap` + `ui.Image` | 导航图标、太阳/月亮图标、圆角方块 logo |
| 用户数据目录 | `mygo.App.Path(mygo.PathUserData)`（macOS `~/Library/Application Support/Bowknot Agent`，Windows `%AppData%\Bowknot Agent`，首次使用创建） | 存放 `settings.json` |
| 视图测试 | `ui.NewTester(view, w, h)` 无窗口点击与文本断言 | 骨架、主题与持久化装配测试 |

前置修正：`AGENTS.md` 第 4 节仍写 mygo `v0.5.5`，与 `go.mod` 的 `v0.6.4` 不一致，S0 一并校正，避免后续按错误版本排查。

## 3. 设计规格

### 3.1 主窗口

| 项 | 规格 |
| --- | --- |
| 默认尺寸 | 工作区宽度 × 80%、高度 × 80%，取整；窗口在工作区内居中 |
| 最小值 | 960 × 640；工作区小于该值时以工作区为上限 |
| 记忆 | `StateKey: "main"`，记住位置、尺寸与最大化；首启无记录时使用 80% 默认值 |
| 标题 | `Bowknot Agent`（窗口标题与应用名一致） |
| 初始底色 | `BackgroundColor: "#ffffff"`，避免首帧白屏闪烁；暗色切换后由主题与 `SetBackgroundColor` 同步重绘 |
| 窗口样式 | `TitleBarStyle: mygo.TitleBarHidden` + `HideWindowButtons: true` |
| 圆角与阴影 | 沿用系统窗口阴影；不自绘窗口圆角 |

### 3.2 自绘标题栏

| 区域 | 规格 |
| --- | --- |
| 高度 | 44 DIP，底部 1px 分隔线（`theme.Border`），背景 `theme.Background` |
| 左侧 | 20×20 圆角方块 logo（`theme.Accent` 底 + 白色 `B`）+ 间距 8 + 应用名（13–14，`Bold`） |
| 右侧 | 主题切换按钮 28×28 + 间距 12 + 三色交通灯 |
| 交通灯 | 直径 12，间距 8；关闭 `#ff5f57`、最小化 `#febc2e`、最大化 `#28c840`；悬停显示 `×` / `−` / `+` 符号 |
| 按钮行为 | 关闭 `Close`；最小化 `Minimize`；最大化 `IsMaximized() ? Unmaximize : Maximize` |
| 拖拽 | 标题栏空白区 `.DragWindow()`；按钮区域不参与拖拽；双击空白区最大化 / 还原 |
| 可访问性 | 图标按钮设置 `Label`（关闭 / 最小化 / 最大化 / 切换主题），供测试与辅助技术识别 |

### 3.3 骨架布局

| 区域 | 规格 |
| --- | --- |
| 结构 | 横向：左侧导航 220 DIP（`theme.Surface`，右侧 1px `theme.Border`）+ 右侧内容区（`Grow(1)`，`theme.Background`） |
| 导航项 | `store` App Store、`agent` 助手 / Agent、`creation` 创作、`settings` 设置，每项一个 `ui.ButtonBase`，`Padding(8,10)`、`Gap(8)`、`Radius` |
| 选中态 | 选中项**始终** `Background(theme.Accent)` + `TextColor(theme.AccentText)`，图标同步为 `AccentText`，不随窗口失焦变灰 |
| 未选中态 | 悬停 `theme.SurfaceHover`，按下 `theme.SurfacePressed`，文字 `theme.Text`、图标 `theme.TextMuted` |
| 图标 | 四个单色 SVG（Lucide 风格：商店、机器人、调色板、齿轮），`ui.MustParseSVG` 一次解析，图标随文字颜色 |
| 默认选中 | `store` |
| 键盘 | 整列 `FocusGroup(ui.Vertical)` 作为单一 Tab 停靠点，方向键移动焦点，Enter / Space 切换选中 |
| 可访问性 | 每项 `Label` 为模块名，`Selected(id == current)` 标记当前项，供测试与辅助技术识别 |
| 内容区 | 模块标题（24，`Bold`）+ 一行说明（`TextMuted`）+ 居中占位卡片（`theme.Surface`、`theme.Border`、`Radius`） |
| 切换行为 | 点击 / Enter / Space 写入选中 ID，内容区按 ID 重建；窗口不重建，选中项保留 |
| 窄窗口 | 侧栏固定 220，内容区可收缩；最小宽度 960 下不出现横向滚动 |

说明：不使用 `ui.Sidebar`。其选中态在窗口失焦时由 mygo 渲染为灰色（`SidebarItem` 的 `chosen && !focused` 分支），与 C-02 的常亮橙色冲突。

### 3.4 主题令牌

| 令牌 | 亮色 | 暗色 | 用途 |
| --- | --- | --- | --- |
| `Background` | `#ffffff` | `#18181b` | 主窗口与内容区底色 |
| `Surface` | `#fafafa` | `#27272a` | 侧栏、占位卡片、按钮面 |
| `SurfaceHover` | `#f4f4f5` | `#3f3f46` | 悬停面 |
| `SurfacePressed` | `#e4e4e7` | `#52525b` | 按下态 |
| `Border` | `#e4e4e7` | `#3f3f46` | 分隔线、卡片描边 |
| `Text` | `#18181b` | `#f4f4f5` | 主文本 |
| `TextMuted` | `#71717a` | `#a1a1aa` | 次级文本、说明 |
| `Accent` | `#f97316` | `#f97316` | 主题色（橙）、选中、主按钮 |
| `AccentHover` | `#ea580c` | `#fb923c` | 强调悬停 |
| `AccentPressed` | `#c2410c` | `#ea580c` | 强调按下 |
| `AccentText` | `#ffffff` | `#ffffff` | 强调上的文字 |
| `Radius` / `Spacing` / `FontSize` | 8 / 4 / 14 | 同左 | 圆角、间距单位、系统字号 |

应用方式：`internal/ui/theme.go` 提供 `Theme(mode)`，两套主题各构建一次；根视图每帧 `c.SetTheme(...)`；标题栏、侧栏、内容区一律取令牌，禁止硬编码颜色。默认 `Light`，切换入口为标题栏主题按钮（浅色显示月亮、暗色显示太阳）。

### 3.5 主题持久化

| 项 | 规格 |
| --- | --- |
| 存放位置 | `mygo.App.Path(mygo.PathUserData)` 下的 `settings.json`；目录由 mygo 首次创建，不写入仓库 |
| 文件格式 | `{"theme":"light"}` 或 `{"theme":"dark"}`，UTF-8 JSON；字段缺失视为亮色 |
| 读取时机 | 创建窗口之前读取一次；文件不存在、JSON 非法、值未知一律回退 `light`，记录一次日志，不阻断启动 |
| 写入时机 | 主题按钮切换后立即写入；写入失败不回滚当前主题，仅记录日志，下次切换重试 |
| 原子性 | 先写 `settings.json.tmp` 再 rename，避免半写文件；Windows rename 失败重试一次，仍失败则丢弃临时文件 |
| 线程 | 读取与写入都发生在主线程（启动装配与按钮回调），不加锁；后续加入更多设置项时再抽独立设置服务 |
| 窗口底色 | 切换主题时同步 `Window.SetBackgroundColor`（亮 `#ffffff`、暗 `#18181b`），避免缩放瞬间露出旧底色 |
| 测试 | `internal/app/settings_test.go` 表驱动，使用 `t.TempDir()`，覆盖缺失 / 合法 / 损坏 / 未知值 / 读取失败 / 写入失败 |

## 4. 优先级评估与实施顺序

结论：**先做主题令牌，再做自绘标题栏（最高风险），接着窗口参数与骨架，最后接持久化与清理**；持久化依赖主题与标题栏按钮，放在骨架之后成本最低。

| 优先级 | 内容 | 理由 |
| --- | --- | --- |
| P0 | 主题令牌与切换骨架 | 所有可视元素都依赖它；先定令牌可避免标题栏、侧栏各写一套颜色后返工 |
| P0 | 自绘标题栏 + 三色灯 + 拖拽 | 唯一可能推翻整窗布局的系统能力：按钮隐藏、拖拽区、边缘缩放、最大化后布局都需实测；失败则要改窗口方案 |
| P0 | 窗口 80% 尺寸、最小尺寸、`StateKey` | 与窗口创建参数同批修改，成本最低且影响截图与布局基线 |
| P1 | 四模块骨架布局（自定义导航，常亮橙选中）与占位内容 | 纯静态排版，风险低；在窗口壳稳定后一次成型，返工最少 |
| P1 | 主题持久化 | 依赖主题模式与标题栏按钮；接口确定后一次接入，避免先写后改 |
| P1 | 视图测试与图标 / 可访问性标签 | 满足 DoD；`Label` 同时服务测试与辅助技术 |
| P2 | 跟随系统外观、失焦置灰、macOS 全屏 | 不阻塞骨架，放入 003 或设置模块 |

不建议先做：侧栏与内容占位排版。它最直观但最容易被标题栏结论推翻，先做会白改一轮。

## 5. 交付物与验收

| 编号 | 交付物 | 要求 | 验收方式 |
| --- | --- | --- | --- |
| D1 | `internal/ui/theme.go` | `Theme(mode)`、亮暗两套令牌、橙色强调、默认亮色 | 单测断言关键色值与 `Dark` 标记 |
| D2 | `internal/ui/titlebar.go` | 左 logo + 标题，右主题按钮 + 三色灯；拖拽、双击、按钮行为、`Label` | 视图测试断言文本与标签；手动验证拖拽与缩放 |
| D3 | `internal/shell/` | `ButtonBase` 自定义导航四模块、常亮橙选中、默认 `store`、内容占位与切换 | 视图测试点击切换后内容变化、选中项样式不随焦点变化 |
| D4 | `internal/app/app.go` | 80% 工作区、居中、最小尺寸、`StateKey`、隐藏系统标题栏与按钮、初始白底 | `windowSize` 纯函数单测；`go tool mygo dev` 手动验证 |
| D5 | `internal/ui/icons/` | 四个导航 SVG + 太阳/月亮 SVG，`go:embed` 一次解析 | 编译通过，图标显示正常 |
| D6 | 首页替换 | 删除 `internal/ui/home.go` 及其测试，由 shell 接管 | `go test ./...` 通过，无残留引用 |
| D7 | `AGENTS.md` 校正 | mygo 版本改为 `v0.6.4`，命令与 `go.mod` 一致 | 版本号与 `go.mod` 一致 |
| D8 | `internal/app/settings.go` | 主题偏好读取 / 原子写入、损坏回退亮色、缺失回退亮色 | 表驱动单测覆盖 3.5 的六种情况 |
| D9 | 本计划 | 状态、决策与验收结果更新 | 文档状态准确 |

## 6. 实施步骤

| 步骤 | 内容 | 完成标志 |
| --- | --- | --- |
| S0 | 校正 `AGENTS.md` 的 mygo 版本 | 与 `go.mod` 一致 |
| S1 | 写 `theme.go`：两套令牌 + `Theme(mode)` + 单测 | 单测通过，色值与第 3.4 节一致 |
| S2 | 标题栏 spike：`TitleBarHidden` + `HideWindowButtons` + `DragWindow` + 三个 `ButtonBase` 圆点 | `go tool mygo dev` 中可拖动、可最小化/最大化/关闭，点击不误触拖拽 |
| S3 | 标题栏成型：logo、应用名、主题按钮、交通灯悬停符号、`Label` | 视觉与交互符合第 3.2 节 |
| S4 | 窗口参数：`windowSize` 纯函数 + 居中 + 最小尺寸 + `StateKey` + 初始白底 | 单测通过；首启 80%，重启恢复上次尺寸 |
| S5 | 骨架：`internal/shell` 自定义导航 + 内容占位 + 默认选中 | 四模块可切换、窗口不重建、选中项常亮橙色 |
| S6 | 持久化：`internal/app/settings.go` + 启动读取 + 切换写入 + 单测 | 重启保持主题；损坏文件回退亮色；单测通过 |
| S7 | 清理：删除 `home.go` 与旧测试，接入 shell | `go test ./...` 通过 |
| S8 | 自检与文档：`gofmt -l .`、`go vet ./...`、`go test ./...`、`go tool mygo build`；更新计划状态 | 全部通过，产物在 `build/windows-amd64/` |

## 7. 验收标准

- 主窗口首次启动为工作区 80% × 80% 并居中；小于 960×640 的工作区不溢出；重启后恢复上次位置与尺寸。
- 标题栏无系统标题栏与系统按钮；左侧 logo + `Bowknot Agent`，右侧主题按钮 + 三色灯位置与规格一致。
- 拖动标题栏空白可移动窗口，双击最大化 / 还原；三色灯分别关闭、最小化、最大化 / 还原；窗口四边可缩放。
- 主题按钮在亮 / 暗间即时切换，主背景由白变深；所有可见颜色来自主题令牌，无硬编码色值。
- 切换主题后重启仍保持上次主题；`settings.json` 缺失、损坏或值未知时回退亮色且不崩溃。
- 侧栏四个模块可点击切换，默认 App Store；右侧显示对应占位内容；选中项在窗口失焦时仍为橙色。
- `gofmt -l .` 无输出，`go vet ./...`、`go test ./...` 通过，`go tool mygo build` 成功。

## 8. 风险与待确认

| 编号 | 事项 | 影响 | 当前处理 |
| --- | --- | --- | --- |
| R-01 | 拖拽区与按钮点击冲突 | 三色灯可能点不动或拖动误触 | S2 先验证；按钮区域不挂 `DragWindow`，只让空白区拖拽 |
| R-02 | `HideWindowButtons` 后最大化状态的标题栏布局 | 最大化后按钮或 padding 偏移 | 用 `IsMaximized` 驱动布局，S2/S3 手动验证 |
| R-03 | macOS 隐藏系统交通灯后的语义差异 | 失去系统全屏按钮与失焦置灰 | **已确认接受**：002 自绘 green 仅最大化 / 还原；全屏与失焦置灰列入 003 |
| R-04 | 暗色切换时窗口外围底色 | 拖动或缩放瞬间露出白底 | 切换时同步 `SetBackgroundColor`；S2/S6 验证 |
| R-05 | `ui.Sidebar` 选中态由 mygo 决定，失焦变灰 | 无法满足常亮橙色 | **已确认改为自定义**：`ButtonBase` 列表；仅剩焦点环与选中态叠加的视觉细节，S5 验证 |
| R-06 | 主题持久化失败 | 启动读失败可能阻断或回退不一致 | 读取失败一律回退 `light` 且不阻断；写入失败不阻塞 UI，保留当前主题 |
| R-07 | logo 尚无正式资源 | 先用圆角方块 + `B` 占位 | 正式 logo 资源到位后替换，不改变布局 |
| R-08 | macOS 本机不可验证 | 标题栏、缩放、DPI 行为未知 | Windows 先验收，macOS 列入跨平台验证清单 |

## 9. 后续计划候选

- 003：顶部全局状态（网关、模型、用量）+ 跟随系统外观 + 失焦态与全屏语义。
- 004：托盘、单实例、关闭到托盘、窗口状态恢复的完整行为（FR-SHELL-02/03）。
- 005：App Store 列表与详情占位数据（FR-APP-01），开始填实第一个模块。
