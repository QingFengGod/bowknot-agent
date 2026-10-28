# 005 App Store 本地安装、WebApp 容器与统一能力集计划

| 项 | 内容 |
| --- | --- |
| 计划编号 | 005 |
| 状态 | 实施中（代码完成，待人工验收） |
| 更新日期 | 2026-10-27 |
| 关联需求 | FR-APP-03 本地安装、FR-APP-04 应用清单、FR-APP-05 启动与隔离、FR-APP-06 统一能力集、FR-APP-07 卸载、FR-APP-08 崩溃隔离、FR-SHELL-02 应用独立窗口、requirements 6.1 能力模型 |
| 前置计划 | 004 侧栏状态位、应用启动器与 App Store 骨架（实施中，人工验收待完成） |
| 目标版本 | v0.1.0-dev |
| 范围 | `.bkapp` 本地安装、WebApp 独立窗口、统一能力集、卸载、启动器交互、错误与崩溃处理 |

## 1. 目标

### 1.1 本次交付

1. 定义 `.bkapp` 安装包格式：**未压缩 tar**，扩展名 `.bkapp`，清单 `manifest.json` 位于包根。
2. 定义应用清单 v1 字段与校验规则，解决 FR-APP-04 的 `[TBD]`。
3. 本地安装：选择 `.bkapp` 或本地目录，校验后原子落盘到 `{DataDir}/apps/<id>/`，失败清理临时文件。
4. 安装索引：扩展注册表的写入能力，状态覆盖安装、运行、损坏与禁用。
5. WebApp 独立窗口：无边框自绘标题栏 + 内嵌 webview，单应用单窗口，记忆尺寸与位置。
6. 本地协议：注册 `bkapp` scheme，按应用 ID 提供包内文件，路径限制在应用根目录；页面地址统一为 `bkapp://localhost/<id>/...`。
7. 统一能力集：应用私有目录读写、显式选择文件/目录、剪贴板、系统通知、打开外链。
8. 能力桥：按调用窗口识别应用身份，统一返回结构化错误，通过预注入脚本暴露 `window.bowknot`。
9. 卸载：确认后关闭窗口，询问保留或删除应用数据，移除程序。
10. 错误与崩溃：加载失败错误页可重试；连续崩溃提示禁用。
11. 启动器交互：卡片主操作启用，新增安装入口、进度与失败原因。
12. 单元测试、文档回写、自检与人工验收步骤。

### 1.2 非目标

- 不做远程商店源、下载、签名与完整性校验（FR-APP-02 后移）。
- 不做应用更新（FR-APP-09）与「可更新 / 更新中」状态的真实流转。
- 不做开发者验证环境（FR-APP-12）。
- 不做应用图标渲染，继续使用名称首字母块。
- 不做真实商店目录与远程 URL 应用（FR-APP-10）。
- 不做应用管理页、数据管理中心与占用统计（FR-APP-11、FR-PRIV-02）。
- 不做会话隔离保证；只承诺每应用独立 `appdata/<id>`。所有应用共用 `bkapp://localhost` origin，包内文件可互相读取。
- 不做同一应用多窗口、应用间通信、深链与命令行参数传递。
- 不做能力授权界面；能力集在安装后即全部可用。
- 不做 macOS 实机验收，Windows 先验收。

### 1.3 已确认决策（2026-10-27）

| 编号 | 决策 | 落点 |
| --- | --- | --- |
| C-01 | 005 主路线为 **WebApp 本地闭环**：安装 → 启动 → 能力集 → 卸载 | 1.1、3.3、3.5 |
| C-02 | 安装包格式为**未压缩 tar**，扩展名 `.bkapp`；取代讨论阶段的 zip 方案 | 3.1、3.3、R-07 |
| C-03 | 本地目录安装保留，作为 `.bkapp` 之外的调试与验收入口 | 3.3、R-06 |
| C-04 | 首版统一能力集**五项全做** | 3.7 |
| C-05 | 005 包含卸载，开发者模式后移 | 1.1、3.8 |
| C-06 | WebApp 窗口使用**无边框自绘标题栏**：`TitleBarHidden` + `HideWindowButtons` + 原生 `Content` 内嵌 `ui.WebView`，不用 `Frameless` | 3.5、R-04 |
| C-07 | 每应用单窗口，重复打开聚焦已有窗口 | 3.5 |
| C-08 | 会话隔离先写入限制说明：只承诺独立 `appdata/<id>`，不承诺 origin、cookie 或站点存储隔离 | 3.6、3.7、R-01 |
| C-09 | 清单版本定为 v1，字段与校验写入本计划并回写 FR-APP-04 | 3.2、D12 |
| C-10 | 应用私有数据放 `{DataDir}/appdata/<id>/`，与程序目录分离，卸载时可选择保留 | 3.3、3.8 |
| C-11 | 已安装 / 运行中的应用拒绝重复安装；损坏 / 已禁用应用允许覆盖重装以修复 | 3.3 |
| C-12 | 崩溃阈值暂定同会话连续 3 次，只提示禁用，不自动改状态 | 3.5、R-08 |
| C-13 | 自定义协议名定为 `bkapp`，统一使用 `bkapp://localhost/<id>/...`；Windows 映射与 macOS 直连的差异由 mygo 内部处理 | 3.6、R-02 |
| C-14 | 取消 S0 技术验证：该组合此前已由 AI 实现过，直接进入正式实现 | 2、4、6 |

## 2. 前置状态与 mygo API 核实

004 已交付：应用模块「已安装 / 发现」双标签、只读注册表、内置示例目录与筛选；卡片主操作全部禁用。`internal/gateway` 仍为空。

| 能力 | mygo 0.6.4 结论 | 影响 |
| --- | --- | --- |
| 独立 web 窗口 | `WindowOptions.URL` 创建纯网页窗口；`WindowOptions.Content` 创建原生 UI 窗口，两者可共存 | WebApp 采用原生 UI + 内嵌 webview，以便自绘标题栏与错误页 |
| 内嵌 webview | `Window.NewWebView(mygo.WebViewOptions)` 要求窗口有 `Content`；`ui.WebView(c, v)` 放置并占据内容区 | 页面只在 `Content` 窗口内可用 |
| 窗口外观 | `TitleBarStyle: TitleBarHidden` + `HideWindowButtons` 保留系统缩放与贴边，标题栏由视图自绘 | 与主窗口一致，不引入 `Frameless` 的缩放问题 |
| 自定义协议 | `mygo.Protocol.Handle(scheme, handler)`，须在创建窗口前注册；URL 必须是 `<scheme>://localhost/<path>`。Windows 由 mygo 映射为 `http://<scheme>.localhost/<path>` 并走 WebView2 资源拦截，macOS 直接使用自定义 scheme | 注册一次 `bkapp`，应用 ID 放在 path，不放 host |
| 协议信任 | 自定义 scheme、`file:` 与 `about:` 页面默认可调用绑定方法 | 无需为每个应用配置 `TrustedOrigins` |
| IPC | `mygo.Bind` / `BindAs` 绑定 Go 服务；页面侧底层为 `window.mygo.call("Service.Method", ...args)` | 能力桥与预注入脚本的基础 |
| 调用方身份 | `mygo.CallerWindow(ctx)` / `CallerPage(ctx)` 取得调用页面所属窗口 | 按窗口反查应用 ID，阻止跨应用访问 |
| 预注入脚本 | `PageOptions.PreloadScript` 在页面脚本前执行，此时 `window.mygo` 已可用 | 注入 `window.bowknot` 包装层 |
| 加载失败 | `Page.OnDidFailLoad`、`OnDidFinishLoad`、`OnDOMReady` | 加载态与错误页 |
| 渲染进程崩溃 | `Page.OnRenderProcessGone(func(reason string))` | 崩溃错误页与计数 |
| 开发工具 | `PageOptions.DevTools`、`Page.OpenDevTools` | 005 不开放，留给开发者模式 |
| 窗口状态 | `WindowOptions.StateKey` 记忆位置、尺寸与最大化状态 | 每应用一个 key |
| 文件选择 | `mygo.Dialog.Open(OpenDialogOptions)`，支持 `Filters` 与 `Directory` | 安装包、目录与能力集的文件选择 |
| 剪贴板 | `mygo.Clipboard.ReadText` / `WriteText` | 能力集 |
| 通知 | `mygo.NewNotification(NotificationOptions).Show()`、`NotificationsSupported()` | 能力集 |
| 外链 | `mygo.Shell.OpenExternal(url)` | 能力集 |
| 会话分区 | 未发现会话分区或独立存储 API | 所有应用共用 `bkapp://localhost` origin，cookie 与站点存储也共用，记入 R-01 |

关键结论：WebApp 容器与能力集在 mygo 0.6.4 上有对应 API；「内嵌 webview + 自定义协议 + Bind」的组合此前已由 AI 实现过，本计划不再安排 S0 技术验证，直接进入正式实现。唯一需要遵守的是协议 URL 的 `localhost` 约定：所有平台统一构造 `bkapp://localhost/<id>/...`，Windows 的 `http://bkapp.localhost` 变换只由 mygo 内部处理。

## 3. 设计规格

### 3.1 `.bkapp` 包格式

| 项 | 规格 |
| --- | --- |
| 容器 | 未压缩 tar（`archive/tar`），不做 gzip / zstd |
| 扩展名 | `.bkapp` |
| 根布局 | `manifest.json` 必须位于包根，不允许外层包装目录 |
| 允许条目 | 普通文件与目录 |
| 拒绝条目 | 符号链接、硬链接、字符/块设备、FIFO、绝对路径、含 `..`、含反斜杠、空路径、Windows 保留名（`CON`、`PRN`、`AUX`、`NUL`、`COM1`–`COM9`、`LPT1`–`LPT9`） |
| 路径规则 | 使用 `/` 分隔；长度 ≤ 255；深度 ≤ 16；大小写不敏感去重，防止 Windows 覆盖 |
| 体积限制 | 总解压后 ≤ 200 MiB（`[TBD]`），单文件 ≤ 64 MiB，条目数 ≤ 2000 |
| 权限 | 忽略归档中的 mode，目录 0755、文件 0644，不保留可执行位 |
| 解压方式 | 逐条流式写入 staging，写入过程中再次校验最终路径仍位于 staging 根内 |

包结构示例：

```text
manifest.json
index.html
assets/app.js
assets/app.css
icon.svg
```

### 3.2 应用清单 v1

```json
{
  "schema": 1,
  "id": "docs",
  "name": "文档工作台",
  "version": "1.4.0",
  "entry": "index.html",
  "icon": "icon.svg",
  "min_shell_version": "0.1.0",
  "summary": "笔记与文档",
  "category": "efficiency",
  "release_notes": "首个本地安装版本。",
  "window": {
    "width": 1080,
    "height": 720,
    "min_width": 640,
    "min_height": 480
  }
}
```

| 字段 | 必填 | 规则 |
| --- | --- | --- |
| `schema` | 是 | 必须为 `1`；未知版本拒绝安装 |
| `id` | 是 | `^[a-z][a-z0-9-]{1,63}$`，全局唯一，安装后不可改 |
| `name` | 是 | 1–64 字符，去首尾空格后非空 |
| `version` | 是 | `^\d+\.\d+\.\d+$`，不支持预发布与构建元数据 |
| `entry` | 是 | 相对路径，必须是包内存在的普通文件，扩展名 `.html` / `.htm` |
| `icon` | 是 | 相对路径，必须存在，`.svg` / `.png` / `.jpg` / `.jpeg` / `.webp`，≤ 1 MiB；005 只校验不渲染 |
| `min_shell_version` | 是 | 与壳版本同格式；高于当前壳版本时拒绝安装并说明 |
| `summary` | 是 | ≤ 200 字符 |
| `category` | 是 | `efficiency` / `creation` / `developer`，未知值显示为「其他」 |
| `release_notes` | 否 | ≤ 500 字符 |
| `window` | 否 | 缺省 1080×720、最小 640×480；宽高 320–4096，最小尺寸不得大于默认尺寸 |
| 未知字段 | — | 忽略，保证向后兼容 |

校验规则：

- 清单文件 ≤ 64 KiB，UTF-8，JSON 解析失败即拒绝。
- 所有相对路径按 `/` 解析，禁止绝对路径、`..`、反斜杠与驱动器前缀。
- 安装前检查 `entry` 与 `icon` 在包内存在；安装后再检查一次，防止归档与实际内容不一致。
- 壳版本常量新增于 `internal/app`（`Version = "0.1.0"`），比较按三段数字逐段进行。
- `id` 冲突按 C-11 处理。

### 3.3 安装流程与目录布局

```text
{DataDir}/
  apps/
    registry.json
    .staging/            # 安装临时目录，启动时清理
    <id>/
      manifest.json
      index.html
      ...
  appdata/
    <id>/                # 应用私有数据，卸载时可选保留
```

| 阶段 | 行为 |
| --- | --- |
| 选择来源 | 文件选择过滤 `*.bkapp`；目录选择以 `Directory: true` |
| 预校验 | 读取并校验清单；目录安装先确认清单存在，包安装先定位根 `manifest.json` |
| 写入 staging | `.bkapp` 逐条解压；目录递归复制，跳过并拒绝符号链接；全程在工作 goroutine 中执行 |
| 落盘 | staging 校验通过后 `os.Rename` 到 `apps/<id>`；重装损坏 / 禁用应用时先移除旧程序目录，保留 appdata |
| 写入索引 | 计算展示信息与体积，状态置「已安装」，来源记为「本地安装」或「目录安装」，原子写回注册表 |
| 失败 | 删除 staging，不写索引，返回分类原因（清单非法、版本不兼容、路径非法、体积超限、磁盘失败、ID 冲突） |
| 启动清理 | 删除 `.staging` 下残留目录；忽略删除失败并记录日志 |

约束：

- 已安装或运行中的同一 `id` 拒绝安装，提示「已安装，更新功能尚未开放」。
- 损坏或已禁用的同一 `id` 允许重装；成功后状态回到「已安装」。
- 安装期间状态「安装中」只保存在内存，不写注册表；进程退出后不会留下中间态。
- 安装任务运行在 goroutine，视图只读状态并显示进度，禁止在视图函数中做 IO。
- 目录安装必须先复制到 `apps/<id>`，不得就地引用用户选择的目录，避免卸载删除源文件。

### 3.4 安装索引与状态流转

| 项 | 规格 |
| --- | --- |
| 存储 | 沿用 `{DataDir}/apps/registry.json` 与现有 `AppInfo` 字段 |
| 路径来源 | 程序目录由 `apps/<id>` 推导，不写入注册表；入口由清单读取 |
| 权威 | 清单为展示与启动权威；注册表保存展示缓存、状态与最近使用时间 |
| 写入 | 新增 `SaveRegistry`，临时文件 + `os.Rename` 原子替换；写入失败保留内存状态并返回错误 |
| 启动校验 | 逐条检查 `apps/<id>/manifest.json`；缺失或解析失败时内存状态置「损坏」 |
| 重启归一 | 注册表中的「运行中」「更新中」「安装中」在启动时归一为「已安装」 |
| 最近使用 | 打开窗口时写 `last_opened`（Unix 秒），关闭窗口不修改 |
| 损坏修复 | 启动器「修复」触发安装流程并覆盖程序目录 |

### 3.5 WebApp 窗口

| 项 | 规格 |
| --- | --- |
| 窗口选项 | `TitleBarStyle: TitleBarHidden`、`HideWindowButtons: true`、`Content: mygoui.View(...)`；不使用 `Frameless` |
| 视图结构 | 顶部自绘标题栏（44 高），下方内容区放置 `ui.WebView` |
| 标题栏 | 左侧字母图标 + 应用名；右侧最小化、最大化 / 全屏、关闭；空白区域拖拽，双击最大化 / 还原 |
| 标题栏焦点态 | 窗口失焦时三个按钮置灰，聚焦恢复颜色 |
| 默认尺寸 | 清单 `window` 优先；缺省 1080×720，最小 640×480 |
| 状态记忆 | `StateKey: "webapp-<id>"`；恢复失败或越界时回到默认布局 |
| 单实例 | 应用 ID → 窗口映射；再次打开只 `Show` + `Focus`，不新建 |
| 页面地址 | 由 `AppURL(id, entry)` 生成 `bkapp://localhost/<id>/<entry>`，不在窗口代码里拼字符串 |
| 页面加载 | 创建窗口后异步 `NewWebView`，页面加载完成前显示「正在加载 <name>」原生覆盖层 |
| 错误页 | `OnDidFailLoad` 显示错误覆盖层：原因 + 「重新加载」；不关闭窗口 |
| 崩溃 | `OnRenderProcessGone` 显示崩溃覆盖层；同会话连续 3 次时追加「禁用应用」按钮 |
| 禁用动作 | 写注册表状态「已禁用」、关闭窗口；启动器卡片显示危险色与「启用」 |
| 关闭 | 销毁 webview，状态「运行中」回落「已安装」并写回注册表 |
| 主窗口退出 | 随应用退出关闭所有 WebApp 窗口，不做拦截 |
| 主题 | 跟随主界面当前主题；标题栏使用 `internal/ui` 令牌，页面自身配色由应用决定 |

实现要点：

- 抽出通用标题栏组件 `ui.WindowTitleBar`，主界面继续使用带 logo 与主题按钮的版本，WebApp 使用仅含应用名与窗口按钮的版本。
- 覆盖层与 webview 在同一原生视图中绘制在 webview 之后，保证错误与加载信息显示在页面上方。
- 窗口与 webview 的创建必须在主线程回调中完成；安装与文件复制在工作 goroutine。

### 3.6 本地协议 `bkapp://`

| 项 | 规格 |
| --- | --- |
| 注册时机 | `App.Run` 中、创建任何窗口之前执行一次 `mygo.Protocol.Handle("bkapp", handler)` |
| 地址 | 统一逻辑地址 `bkapp://localhost/<id>/<path>`；应用 ID 放在路径第一段，不放 host |
| 平台差异 | Windows 由 mygo 转成 `http://bkapp.localhost/<id>/<path>` 后走 WebView2 资源拦截；macOS 直接加载 `bkapp://localhost/...`。业务代码只构造 `bkapp://localhost/...`，不出现 `http://bkapp.localhost` |
| URL 生成 | `internal/store/protocol.go` 提供 `AppURL(id, entry)` 与 `AppRootURL(id)`；视图、清单跳转与测试统一调用，禁止各处手写 |
| 路由 | 解析 `req.URL` 的 path，第一段为应用 ID，其余为包内相对路径；只服务已安装应用，未知 ID 返回 404 |
| 路径安全 | 去掉应用 ID 后 `path.Clean`，拒绝绝对路径与 `..`；解析后必须位于 `apps/<id>` 内；只允许普通文件 |
| 目录请求 | 不做目录列表；`/` 不自动映射到 `index.html`，入口由清单显式给出 |
| MIME | 按扩展名映射；HTML / JS / CSS 补 `charset=utf-8`；未知类型用 `application/octet-stream` |
| 缓存 | `Cache-Control: no-store`，避免重装后读到旧文件 |
| 方法 | 支持 `GET` / `HEAD`，其余返回 405 |
| 错误 | 404 与 405 返回简短文本，不暴露文件系统路径 |
| Origin | 所有应用共用 `bkapp://localhost`；应用包内文件可被其他应用页面请求，仅私有数据由能力集隔离 |

约束：

- 页面内的相对链接与资源引用以 `bkapp://localhost/<id>/` 为基准，直接使用相对路径即可。
- 不允许写成 `bkapp://<id>/...`：Windows 只改写 host 为 `localhost` 的地址，其他 host 不会被 mygo 识别。
- 若后续要按应用拆分 origin，需改为每应用独立 scheme 或独立端口，不在本期。

### 3.7 统一能力集

绑定服务名 `BowknotCapability`，所有方法首参为 `context.Context`，通过 `mygo.CallerWindow(ctx)` 反查应用 ID；无法识别调用方时返回 `unauthorized`。

| 能力 | 页面 API | 规则 |
| --- | --- | --- |
| 私有目录 | `bowknot.appData.read(path)` / `write(path, text)` / `list(path)` / `remove(path)` | 根为 `appdata/<id>`；相对路径，禁止绝对路径与 `..`；文本 UTF-8，单次 ≤ 1 MiB；`list` ≤ 500 条 |
| 显式选择文件 | `bowknot.files.pickFile()` / `pickDirectory()` | 打开系统选择器，返回 `{token, name, isDirectory}`；页面拿不到绝对路径 |
| 受限读写 | `bowknot.files.readGranted(token, relative)` / `writeGranted(token, relative, text)` / `listGranted(token, relative)` | 只允许令牌对应的文件或目录根内；令牌绑定窗口，窗口关闭即失效；每窗口最多 32 个令牌 |
| 剪贴板 | `bowknot.clipboard.readText()` / `writeText(text)` | 文本 ≤ 1 MiB |
| 通知 | `bowknot.notify({title, body})` | 标题 ≤ 64、正文 ≤ 256；系统不支持时返回 `unsupported` |
| 外部链接 | `bowknot.openExternal(url)` | 只允许 `http` / `https` / `mailto`，其余返回 `denied` |

错误模型：

- 所有能力方法返回 `{ok, code, message, data}`；预注入脚本在 `ok=false` 时抛出带 `code` 的 `Error`。
- 错误码：`unauthorized`、`invalid_argument`、`out_of_root`、`not_found`、`too_large`、`denied`、`unsupported`、`internal`。
- 错误信息不包含绝对路径、令牌内容或宿主内部细节。
- 非预期错误统一转 `internal`，并记录本地日志（不记录用户正文）。

预注入脚本：

- `PageOptions.PreloadScript` 中定义 `window.bowknot`，内部调用 `window.mygo.call("BowknotCapability.<Method>", ...)`。
- 包装层负责参数默认值、结果解包与错误码；不向页面暴露 `window.mygo` 之外的宿主对象。
- 页面仍可直接使用 `window.mygo` 的通用能力（如窗口控制），属于 mygo 既有行为；本计划不额外收紧，记入 R-10。

身份与隔离：

- 窗口创建时把 `Window` 与 `id` 注册到宿主映射；窗口关闭时移除并清空该窗口的全部文件授权令牌。
- 能力方法只按调用窗口取索引，不接受页面自报的应用 ID。
- 所有应用共用 `bkapp://localhost` origin；只有 `appdata/<id>` 与能力调用按窗口隔离，见 3.6 与 R-01。

### 3.8 卸载

| 项 | 规格 |
| --- | --- |
| 入口 | 启动器已安装卡片上的「卸载」次操作 |
| 顺序 | 二次确认 → 关闭该应用窗口 → 询问「保留应用数据 / 删除应用数据」→ 移除 `apps/<id>` → 更新注册表 |
| 保留数据 | 只删除程序目录，`appdata/<id>` 保留；当前无数据管理中心，路径记录在人工验收说明中 |
| 删除数据 | 程序目录与 `appdata/<id>` 一并删除 |
| 失败 | 删除失败时保留注册表条目并说明原因；Windows 文件占用时提示稍后重试 |
| 清理 | 卸载后该应用的所有授权令牌失效；应用 ID 可重新安装 |

### 3.9 启动器交互

| 项 | 规格 |
| --- | --- |
| 安装入口 | 「已安装」工具栏新增「安装包」与「从目录安装」两个按钮；空状态同样提供 |
| 选择器 | 安装包过滤 `*.bkapp`；目录选择 `Directory: true` |
| 进行中 | 卡片或顶部显示「安装中 <名称>」，按钮禁用；完成后刷新列表 |
| 失败 | 显示分类原因与「重试」；不产生注册表条目 |
| 主操作 | 已安装 / 运行中 →「打开」；损坏 →「修复」；已禁用 →「启用」；安装中 / 可更新 / 更新中保持禁用并说明 |
| 卸载 | 已安装 / 运行中 / 已禁用 / 损坏卡片提供「卸载」 |
| 发现页 | 内置示例仍不提供安装，详情文案改为「示例应用不含安装包，请使用本地安装」 |

## 4. 优先级评估与实施顺序

结论：**先纯逻辑与安全边界，再协议与能力桥，最后窗口与界面装配**。协议与能力桥是窗口的前置条件；窗口是安装成功后唯一可见的验证路径。

| 优先级 | 内容 | 理由 |
| --- | --- | --- |
| P0 | 清单校验 + tar 安全解压 + 路径函数 | 纯逻辑，表驱动测试成本低，安全边界必须先定 |
| P0 | 安装 / 卸载 / 注册表写入 | 数据契约先行，避免窗口依赖伪造数据 |
| P0 | `bkapp://` 处理器、`AppURL` 与路径沙箱 | 启动窗口的前置条件；URL 形式必须一次定对 |
| P0 | 能力集与预注入脚本 | 五项能力是 FR-APP-06 的 P0 |
| P0 | WebApp 窗口、标题栏与错误覆盖层 | 用户可见主路径 |
| P1 | 启动器交互与安装入口 | 依赖安装与窗口契约 |
| P1 | `app` 装配、文档回写与自检 | 满足 DoD |
| P1 | 人工验收 | 真机行为无法自动化 |

## 5. 交付物与验收

| 编号 | 交付物 | 要求 | 验收方式 |
| --- | --- | --- | --- |
| D1 | `internal/platform/paths.go` | 新增 `AppDataDir()`；补充路径单测 | 单元测试 |
| D2 | `internal/store/manifest.go` | 清单结构、解析、字段与路径校验、版本比较 | 表驱动单测 |
| D3 | `internal/store/archive.go` | `.bkapp` tar 解压、条目白名单、大小与路径限制、目录复制 | 表驱动单测（合法 / 穿越 / 链接 / 重名 / 超限） |
| D4 | `internal/store/install.go` | staging、原子落盘、冲突策略、失败清理、来源与体积计算 | 单元测试 |
| D5 | `internal/store/registry.go` | `SaveRegistry` 原子写入、启动归一与损坏检测 | 单元测试 |
| D6 | `internal/store/protocol.go` | `bkapp://` 处理器、`AppURL` / `AppRootURL`、MIME、404/405、no-store | `httptest` 单测 |
| D7 | `internal/capability/service.go` | 五项能力、`{ok, code, message, data}`、身份反查、路径沙箱、令牌 | 表驱动单测 + 替身 |
| D8 | `internal/capability/preload.go` | `window.bowknot` 预注入脚本与错误码映射 | 脚本快照 + 人工验证 |
| D9 | `internal/store/webapp.go` | 窗口创建、内嵌 webview、单实例、加载态、错误与崩溃覆盖层、状态回写 | 视图测试 + 人工验收 |
| D10 | `internal/ui/titlebar.go` | 抽出通用 `WindowTitleBar`，主界面与 WebApp 共用窗口按钮 | 视图测试 + 人工视觉验收 |
| D11 | `internal/store/page.go` | 安装入口、安装中状态、主操作与卸载入口、失败原因 | `ui.NewTester` 视图测试 |
| D12 | `docs/requirements.md` | FR-APP-04 回写清单 v1 字段与 `.bkapp` 格式 | 文档核对 |
| D13 | `internal/app/app.go` | 注册协议、注入能力服务、装配安装器与窗口宿主 | 编译 + 链路测试 |
| D14 | 测试与文档 | 新增用例、本计划实施记录与人工验收步骤 | `go test ./...` 通过 |

## 6. 实施步骤

| 步骤 | 内容 | 完成标志 |
| --- | --- | --- |
| S1 | `platform` 补 `AppDataDir`；`store` 补清单结构与校验单测 | `go test ./internal/platform ./internal/store` 通过 |
| S2 | `.bkapp` 解压、目录复制与安全测试 | 非法条目全部被拒绝 |
| S3 | 安装 / 冲突 / 失败清理 + 注册表写入与启动归一 | 安装与卸载单测通过 |
| S4 | `bkapp://` 协议处理器、`AppURL` 与路径沙箱测试 | `httptest` 用例通过 |
| S5 | 能力服务、身份反查、令牌与错误码单测 | 跨应用访问被拒绝 |
| S6 | 预注入脚本与 `window.bowknot` 包装 | 最小页面可读写私有目录 |
| S7 | 通用标题栏重构与 WebApp 窗口视图 | `ui.NewTester` 覆盖标题栏与错误覆盖层 |
| S8 | 启动器安装入口、安装中状态、主操作与卸载 | 视图用例通过 |
| S9 | `app` 注册协议、绑定服务、装配宿主 | 编译通过，启动无空页面 |
| S10 | 回写 requirements FR-APP-04；更新本计划实施记录 | 文档一致 |
| S11 | 自检 `gofmt -l .`、`go vet ./...`、`go test ./...`、`go tool mygo build` | 全部通过 |
| S12 | 整理人工验收步骤与示例目录 | 用户可按步骤验收 |

## 7. 验收标准

- 使用 `.bkapp` 安装成功：启动器出现卡片，状态「已安装」，来源显示「本地安装」。
- 使用本地目录安装成功：来源显示「目录安装」，程序已复制到 `apps/<id>`，删除源目录不影响运行。
- 缺清单、清单字段非法、入口不存在、版本高于壳版本、含 `..` 或符号链接的包被拒绝，并显示分类原因；staging 无残留。
- 安装过程中界面保持响应，卡片显示「安装中」，完成后自动刷新。
- 点击「打开」出现自绘标题栏的独立窗口，标题为应用名，页面加载完成后覆盖层消失。
- 再次点击同一应用只聚焦已有窗口，不新建。
- 关闭后重新打开，窗口尺寸与位置按 `StateKey` 恢复；应用重启后最近使用排序生效。
- 页面内 `window.bowknot.appData.write("note.json", "{}")` 成功，重启应用后仍可读取。
- 私有目录路径逃逸（`../`、绝对路径、反斜杠）返回 `out_of_root`，不写入任何文件。
- 选择文件后返回令牌，只能读写该文件；选择目录后只能读写其子路径；窗口关闭后令牌失效。
- 剪贴板文本读写、系统通知、`https` 外链可用；`file:` / `javascript:` 外链返回 `denied`。
- 页面加载失败显示错误页与「重新加载」；重新加载成功后恢复。
- 同会话连续 3 次渲染崩溃后出现「禁用应用」，执行后状态为「已禁用」且窗口关闭。
- 卸载流程按「确认 → 关窗 → 保留或删除数据 → 移除程序」执行；选择保留时 `appdata/<id>` 仍在。
- 删除应用包内 `manifest.json` 后重启，卡片状态变为「损坏」，「修复」可重装。
- 主界面与 WebApp 窗口在亮 / 暗主题下标题栏与按钮可读，窗口缩放与贴边正常。
- 文档写明隔离限制：所有应用共用 `bkapp://localhost` origin，仅 `appdata/<id>` 与能力调用隔离，不承诺 cookie 与站点存储隔离。
- `gofmt -l .` 无输出，`go vet ./...`、`go test ./...` 通过，`go tool mygo build` 成功。

## 8. 风险与待确认

| 编号 | 事项 | 影响 | 当前处理 |
| --- | --- | --- | --- |
| R-01 | mygo 无会话分区 API，且所有应用共用 `bkapp://localhost` origin | cookie、localStorage 跨应用共用；应用包内文件可被其他应用页面请求 | 只承诺 `appdata/<id>` 与能力调用隔离；写入限制说明，不阻塞主线，隔离方案留 009 |
| R-02 | 自定义协议 URL 存在平台差异 | 误写成 `bkapp://<id>/...` 会导致 Windows 无法加载，macOS 表现不同 | 统一用 `bkapp://localhost/<id>/...`，由 `AppURL` 生成；Windows 实机验收加载 |
| R-03 | 应用图标字段必填但 005 不渲染 | 与 FR-APP-04 的展示意图有差距 | 只校验不展示，图标渲染列入后续计划 |
| R-04 | 自绘标题栏与 webview 的层次、拖拽、缩放 | 可能出现遮挡或拖拽吞点击 | 复用主窗口 `TitleBarHidden` 方案；标题栏不覆盖 webview；人工重点验收 |
| R-05 | Windows 文件占用导致卸载删除失败 | 卸载不彻底 | 先关窗再删除；失败时保留条目并提示重试 |
| R-06 | 目录安装复制大目录耗时 | 界面卡顿或进度不明 | 安装在工作 goroutine，视图只读状态 |
| R-07 | tar 安全规则可能拒绝合法应用（符号链接、保留名） | 部分应用无法安装 | 规则写入清单文档；错误信息说明具体条目 |
| R-08 | 崩溃阈值 3 次为暂定值 | 误禁用或频繁打扰 | 只提示不自动禁用；阈值可配置列入后续 |
| R-09 | 无数据管理中心 | 卸载保留的数据暂不可见 | 保留路径写入验收说明；数据中心后续计划 |
| R-10 | 页面仍可直接使用 `window.mygo` | 能力边界比预期宽 | 本计划不收紧；后续评估预注入替换或 CSP |
| R-11 | macOS 通知权限与自定义 scheme 行为未验证 | 能力集在 macOS 可能降级 | Windows 先验收，macOS 列入人工验证清单 |
| R-12 | `.bkapp` 未压缩体积较大 | 安装包分发成本高 | 本期接受；压缩容器作为后续格式扩展 |
| R-13 | 应用无真实安装包 | 验收缺少素材 | 仓库提供最小示例目录用于目录安装；`.bkapp` 由测试与打包步骤生成 |

## 9. 后续计划候选

- 006：AI 网关最小闭环（已立项实施，见 `docs/tasks/006-task.md`），把侧栏状态位接真实数据。
- 007：助手对话最小闭环（已实施，见 `docs/tasks/007-task.md`）；开发者验证环境（FR-APP-12）顺延至后续计划。
- 008：多 Agent 与任务派发最小闭环（已由 `docs/tasks/008-task.md` 接续）。
- 009：应用图标渲染、更新检查、远程安装与签名校验（FR-APP-02、FR-APP-09）。
- 010：会话隔离验证与方案（分区、独立存储或容器化）。
- 011：数据中心与应用管理（FR-APP-11、FR-PRIV-02），让卸载保留的数据可见。

## 10. 实施记录

| 项 | 结果 |
| --- | --- |
| 计划建立 | 2026-10-27，状态「草稿」；等待确认后进入实施 |
| 范围调整 | 2026-10-27，安装包定为未压缩 tar `.bkapp`；协议改名 `bkapp`；按用户确认取消 S0 技术验证 |
| S1 完成 | `platform.AppDataDir()`、`store/manifest.go`（schema 1 校验、路径规则、版本比较）及表驱动单测 |
| S2 完成 | `store/archive.go`：未压缩 tar 解压、目录复制、条目白名单、体积与条目上限、大小写重复检查 |
| S3 完成 | `store/install.go` 与 `registry.go`：staging 原子落盘、冲突策略、失败回滚、注册表原子写、启动归一与损坏检测 |
| S4 完成 | `store/protocol.go`：`bkapp://localhost/<id>/<path>` 处理器、MIME、404/405、`no-store` |
| S5–S6 完成 | `internal/capability`：五项能力、调用窗口身份反查、路径沙箱、令牌、结构化错误码与 `window.bowknot` 预注入 |
| S7 完成 | `ui.WindowTitleBar` 通用标题栏；`store/webapp.go` 单应用单窗口、加载/错误/崩溃覆盖层、崩溃阈值 3 次 |
| S8 完成 | 启动器「安装包…」「从目录安装」、主操作（打开/启用/修复）、卸载入口与发现页说明 |
| S9 完成 | `app` 注册 `bkapp` 协议、绑定 `BowknotCapability`、装配安装器与窗口宿主、主题同步与退出关闭 |
| S10 完成 | `docs/requirements.md` FR-APP-03/04 更新为 `.bkapp` 与 schema 1 字段 |
| S11 通过 | `gofmt -l .` 无输出；`go vet ./...`、`go test ./...`、`go tool mygo build` 全部通过 |
| 示例 | `docs/examples/hello-webapp/`，可直接目录安装或打包为 `.bkapp` |

## 11. 人工验收步骤

验收入口：`go tool mygo dev`，或 `build/windows-amd64/Bowknot Agent.exe`。

1. 安装示例目录
   - 操作：应用 → 已安装 → 「从目录安装」，选择 `docs/examples/hello-webapp`。
   - 预期：出现「已安装 你好 WebApp」，网格出现卡片，状态「已安装」。
2. 打包安装（可选）
   - 操作：`tar -cf hello.bkapp -C docs/examples/hello-webapp .`，再用「安装包…」选择该文件。
   - 预期：同上；若重复安装已安装的同一 ID，提示「该应用已安装，更新功能尚未开放」。
3. 打开与窗口
   - 操作：点击卡片主操作「打开」。
   - 预期：独立窗口打开，自绘标题栏显示应用名与最小化/最大化/关闭；拖动空白可移动，双击可缩放；缩放与贴边正常；再点「打开」只聚焦已有窗口。
4. 能力集
   - 操作：依次点击示例页面的写入/读取、剪贴板读写、通知、外链、选择文件。
   - 预期：写入与读取返回文本；剪贴板往返一致；系统通知出现；浏览器打开 `https://example.com`；选择文件后可读取内容；任何失败都以「code + message」显示，不出现原始文件系统路径。
5. 错误页
   - 操作：把已安装应用目录中的 `index.html` 改名后，再在主界面点击「打开」。
   - 预期：卡片变为「损坏」，主操作变「修复」；不会打开空白窗口。
6. 崩溃提示
   - 操作：在应用窗口内通过 DevTools 或页面脚本触发渲染进程崩溃（可选）。
   - 预期：自动重新加载；同一窗口连续崩溃 3 次后显示「应用无响应」与重新加载/关闭窗口，不自动禁用。
7. 卸载
   - 操作：点击卡片「卸载」→ 确认卸载 → 选择「删除数据」。
   - 预期：窗口先关闭，卡片从网格移除；`{UserHomeDir}/.bowknot-agent/apps/<id>` 与 `appdata/<id>` 均不存在；选择「保留数据」时 `appdata/<id>` 保留。
8. 退出
   - 操作：退出应用。
   - 预期：所有 WebApp 窗口随主窗口关闭，进程结束；重开后“已安装”列表与上次一致。

重点观察：标题栏与页面是否有错位或闪烁、窗口缩放是否卡顿、安装后首次打开是否出现长时间空白、错误覆盖层是否遮挡标题栏按钮、卸载后残留文件。

