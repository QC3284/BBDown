# 上游对齐追踪（Upstream Alignment）

> 本文件是 **Go 重写版与上游 C# 版行为对齐的唯一追踪基线**。
> 目的：把「落后多少版本」变成「缺哪些条目」，并让每次同步都可复现、可交接。
> 建立日期：2026-09-19 ｜ 结论基于 `upstream/master` = v1.6.19。

## 1. 参照源

| 名称 | remote / ref | 版本 | commit | 日期 |
|---|---|---|---|---|
| 上游（权威参照） | `upstream` → aliveranme/BBDown | **v1.6.19** | `2d2573b` | 2026-09-18 |
| 本项目 C# 快照分支 | `origin/master` | v1.6.10 + 2 补丁 | `9fb96f2` | 2026-08-11 |
| Go 重写（工作分支） | `origin/main` | 1.6.11-go | `f0dc0bf` | — |

同步状态：`origin/master` 是 `upstream/master` 的**严格祖先**（`0 ahead / 122 behind`），可纯快进，无分叉、无本地独有提交。

Go 侧自报基线为 **C# v1.6.11**，硬编码于三处：`internal/cli/root.go:119`、`internal/cli/root.go:336`、`cmd/bbdown/main.go:11`。

### 1.1 项目谱系（重要）

真正的原始上游是 **nilaoda/BBDown**；`aliveranme/BBDown` 本身是它的 fork，且已领先原文 **313 个提交**
（依据上游 `docs/FORK_DIFFERENCES.md` 头部快照基准）：

```
nilaoda/BBDown (C# 原版)
  └─ aliveranme/BBDown (fork：深度重构 + DRM + serve API + 4 个子命令 + 测试/CI)
       └─ QC3284/BBDown (本仓库：main = Go 重写，master = C# 快照)
```

**对齐对象是 aliveranme fork，不是 nilaoda。** 含义：

1. 上游 fork 已有成熟的「同步更上游」流程（`FORK_DIFFERENCES.md` §6），本仓库可以照搬同一姿态 —— 我们的对齐义务止于 aliveranme，nilaoda 的增量由上游 fork 消化。
2. 上游 fork §6.2 列出的冲突热点（`Parser.cs`、`HTTPUtil.cs`、`BBDownApiServer.cs`、`MyOption.cs`、`SubUtil.cs`、各 Fetcher）与 §2 的改动热点表**高度重合** —— 即上游重构最深的地方就是本次对账的重点。
3. 上游承诺「相对 nilaoda 的 57 个 CLI 选项零删除」，本仓库 README 列出的选项集与之吻合，说明 Go 侧继承了 fork 的选项面。

### 1.2 上游可复用的对照资产

| 资产 | 用途 |
|---|---|
| `BBDown.Tests/`（659 例） | 行为规格的第二来源；`failSkips: true` 防静默跳过 |
| `BBDown.Tests/FakeBilibiliApiServer.cs` | 假 B 站服务器，集成测试驱动完整录制/下载循环 |
| `BBDown.Tests/Fixtures/parser/*.json` | 20+ 真实响应夹具（`biz-error`/`missing-nodes-tolerant`/`dash-reparse-pass1,2`/`drm-dash-badkid`/`play-limited` …）**可直接移植成 Go 测试** |
| `docs/REVIEW_FINDINGS.md` | RF-1..RF-88 缺陷清单（含已采纳/技术债/维持现状/待议四类） |
| `AGENTS.md` / `CONTRIBUTING.md` / `SECURITY.md` / `CHANGELOG.md` | 工程与流程姿态；本仓库当前只有 `ci.yml`，无 CHANGELOG、无协作规范 |
| `docs/wiki/`（15 页 + `scripts/sync-wiki.ps1`） | 用户文档主体 |

## 2. 差距规模

v1.6.11 → v1.6.19：**8 个 release / 111 个提交 / 163 个文件（+16386 −1892）**。

上游为**三项目结构**：`BBDown/`（CLI 与管线，`Application/*.cs` 是 `partial class Program` 拆分）、`BBDown.Core/`（`Parser.cs`/`Fetcher/`/`DRM/`/`Util/HTTPUtil.cs` 真身）、`BBDown.Tests/`（含 `FakeBilibiliApiServer.cs` 假服务器与 `Fixtures/parser/*.json` 夹具）。该结构在 v1.6.11 已存在，区间内**无新增源文件**（新增的是 `docs/`、`AGENTS.md`、`scripts/`、CI 与测试夹具）。

源码改动热点（变动行 = 新增 + 删除）：

| 上游文件 | 新增/删除 | 变动 | 对应 Go 包 |
|---|---|---|---|
| `Application/Download.cs` | +850 / −507 | 1357 | `internal/download`、`internal/workflow` |
| `BBDown.Core/Util/HTTPUtil.cs` | +686 / −122 | 808 | `internal/util/http.go` |
| `Infrastructure/BBDownApiServer.cs` | +558 / −79 | 637 | `internal/server` |
| `Infrastructure/BBDownDownloadUtil.cs` | +545 / −137 | 682 | `internal/download` |
| `Infrastructure/LiveStreamUtil.cs` | +467 / −107 | 574 | `internal/live` |
| `BBDown.Core/Parser.cs` | +229 / −78 | 307 | `internal/parser` |
| `BBDown.Core/DRM/WidevineCdm.cs` | +171 / −75 | 246 | `internal/drm` |
| `Commands/SubCommand.cs` | +123 / −60 | 183 | `internal/substore` |
| `BBDown.Core/Logger.cs` | +164 / −8 | 172 | `internal/util/logger.go` |
| `Infrastructure/BBDownMuxer.cs` | +127 / −34 | 161 | `internal/muxer` |
| `Utilities/BBDownUtil.cs` | +86 / −42 | 128 | 分散 |
| `Commands/LiveCommand.cs` | +79 / −47 | 126 | `internal/cli`、`internal/live` |
| `Commands/WatchLaterCommand.cs` | +69 / −53 | 122 | `internal/cli` |
| `Infrastructure/BBDownLoginUtil.cs` | +87 / −16 | 103 | `internal/login` |
| `BBDown.Core/Util/PathUtil.cs` | +75 / −3 | 78 | `internal/util/file.go` |

## 3. 可复现的检查步骤

```bash
git fetch upstream --tags
git rev-list --left-right --count origin/master...upstream/master   # 0  ahead / N  behind
git log --oneline origin/master..upstream/master | head -40
git diff --stat origin/master upstream/master

# 逐版本行为要点（1.6.12 ~ 1.6.19）
git show v1.6.19:CHANGELOG.md
# 上游自我审查发现条目 RF-1..RF-88（含采纳/技术债/维持现状/待议）
git show v1.6.19:docs/REVIEW_FINDINGS.md
# C# 实现真相
git diff v1.6.11 v1.6.19 -- BBDown/
```

上游额外产出的可复用资产（Go 仓库尚未对应）：`docs/REVIEW_FINDINGS.md`、`REVIEW_PLAN.md`、
`OPTIMIZATION_PLAN.md`、`FORK_DIFFERENCES.md`、`MAINTENANCE_PLAN.md`、`PROJECT_ANALYSIS.md`、
`docs/wiki/*`（13 篇）、`AGENTS.md`。

## 4. 条目清单与判定

判定口径：

| 标记 | 含义 |
|---|---|
| ✅ | 已对齐 —— Go 有等价实现，附 `文件:行号` |
| ⚠️ | 不等价 —— 部分覆盖或语义不同 |
| ❌ | 缺失 —— 需补，指出应落位置 |
| ➖ | N/A —— C# 特有（culture 敏感、async-over-sync、AOT 绑定、NuGet/CI 门禁、异常类型体系差异等） |
| ❓ | 待验证 —— 静态阅读无法判定，需运行或集成测试 |

审计按 Go 包簇分 5 片并行进行，明细见下表：

| 片 | 范围 | 上游源码 | Go 包 | 报告 |
|---|---|---|---|---|
| A | serve / API 服务器 | `Infrastructure/BBDownApiServer.cs`、`Commands/ServeCommand.cs`、`Program.cs` | `internal/server`、`internal/cli` | `docs/alignment/A-serve.md` |
| B | 下载 / 混流 / 外部程序 / 进度 | `Application/Download.cs`、`Infrastructure/BBDownDownloadUtil.cs`、`BBDownMuxer.cs`、`BBDownAria2c.cs`、`ExternalProcessRunner.cs`、`Utilities/ExternalToolHelper.cs`、`Application/{Archive,PathHelper,Pages,TrackSort}.cs`、`ProgressBar.cs` | `internal/download`、`internal/muxer`、`internal/workflow` | `docs/alignment/B-download-muxer.md` |
| C | live / article / sub / watchlater / login | `LiveStreamUtil.cs`、`Commands/{Live,Sub,WatchLater,Article,Login,LoginTV}Command.cs`、`Infrastructure/{ArticleUtil,SubscriptionStore,BBDownLoginUtil,ConsoleQRCode}.cs`、`BBDown.Core/Util/ServerClock.cs`（新增） | `internal/live`、`internal/article`、`internal/substore`、`internal/login`、`internal/cli` | `docs/alignment/C-live-article-login.md` |
| D | 网络 / 凭据 / URL / 配置 / CLI | `BBDown.Core/Util/HTTPUtil.cs`（真身）、`BBDown.Core/{Logger,AppHelper,Config,AppSettings}.cs`、`Util/PathUtil.cs`、`Utilities/UrlResolver.cs`、`Configuration/{BBDownConfigParser,MyOption}.cs`、`Program.cs` | `internal/util/http.go`、`internal/util/logger.go`、`internal/workflow/resolve.go`、`internal/config`、`internal/cli` | `docs/alignment/D-http-config-cli.md` |
| E | fetcher / parser / DRM / 评论 / 弹幕字幕 | `BBDown.Core/{Parser.cs,Fetcher/*.cs,DRM/*.cs,DanmakuUtil.cs,Entity/Entity.cs}`、`Util/{SubUtil,RiskControlResponseException,BilibiliBvConverter}.cs`、`Infrastructure/CommentUtil.cs` | `internal/fetcher`、`internal/parser`、`internal/drm`、`internal/appapi`、`internal/util/{danmaku,subtitle}.go` | `docs/alignment/E-fetcher-parser-drm.md` |

### 4.1 判定总表（五片已回收，2026-09-19）

| 片 | 范围 | 条目 | ✅ | ⚠️ | ❌ | ➖ | ❓ |
|---|---|---|---|---|---|---|---|
| A | serve / API 服务器 | 33 | 11 | 6 | 15 | 1 | 0 |
| B | 下载 / 混流 / 外部程序 / 进度归档 | 39 | 10 | 10 | 15 | 3 | 1 |
| C | live / article / sub / watchlater / login | 35 | 8 | 5 | 17 | 5 | 0 |
| D | HTTP / 凭据 / URL / 配置 / CLI | 34 | 8 | 8 | 14 | 4 | 0 |
| E | fetcher / parser / DRM / 评论 / 弹幕字幕 | 46 | 13 | 6 | 23 | 4 | 0 |
| **合计** | | **187** | **50** | **35** | **84** | **17** | **1** |

**❌ 84 / 187 ≈ 45%** 的上游行为变更在 Go 侧没有落点；➖ 17 条为 C# 特有（culture 敏感、async-over-sync、AOT、CI 门禁、异常类型体系），天然无需移植。

### 4.2 P0 队列（正确性 / 安全 / 数据丢失）

| # | 条目 | 影响 | 证据（Go 侧） | 验证状态 |
|---|---|---|---|---|
| 1 | **混流输出选项位置错**（B#15，上游 v1.6.16 已修） | 封面+章节 / ≥2 字幕 / 字幕+章节 / 任何背景音轨 → **混流必败** | `internal/muxer/muxer.go:68-110`、`:126-132` | **已双向复现**：审计员 + 本会话独立复现，ffmpeg n9.0.1 `exit=234`；选项移到全部 `-i` 之后即 `exit=0` |
| 2 | **mp4box 分支无 `audioMaterial`**（B#22） | 杜比视界自动切 mp4box 时配音/背景轨静默丢弃，随后被删除 → **永久丢失** | `internal/muxer/muxer.go:24-38`、`:227`；`internal/workflow/workflow.go:807-809` | 代码级确认 |
| 3 | **占位符与 id 路径穿越**（B#30/#31 + E） | `<aid>/<cid>`、`<dfn>/<res>/<fps>/<videoCodecs>/<audioCodecs>`、`lan/audio_id` 裸替换；镜像站 / `--insecure` 中间人可写出 `--work-dir` 之外 | `internal/download/downloader.go:846-861`；`internal/entity/entity.go:9-23`；`internal/util/subtitle.go:72` | 上游 RF-48/58/63/73；**两片独立命中** |
| 4 | **serve 读/写门禁整片缺失**（A#1） | 未配 token 时 `tokenMiddleware` **根本不挂** → 无 Host 校验（rebinding 可读 `/get-tasks` 的 `SavePaths` 绝对路径）+ 无 Origin/Content-Type 校验（CSRF 简单请求可驱动 `/add-task`、`/cancel`） | `internal/server/server.go:156-158`、`:218-231` | **本会话抽验确认**，并发现"无 token 时中间件不安装"这一放大器 |
| 5 | **携凭据请求可被 3xx 引走**（C#2 + D#1） | WEB 轮询 / TV `auth_code`+轮询 / gRPC POST 均未拦 3xx，SESSDATA 与轮询下发的 `access_token` 可被引向任意主机 | `internal/login/login.go:69`、`:149`、`:191`；`internal/util/http.go:193` | D 用本机探针实测：跨主机名标准库剥 Cookie、**同主机名不同端口不剥** → 定 ⚠️ 而非 ❌（RF-13/RF-37 未落地） |
| 6 | **直播录制删除已录内容**（C#1） | `defer os.RemoveAll(segRoot)` 在所有返回路径删掉含历次保留分段的整棵 `.segs`，三处日志却称"已保留"；读中断先删段再判取消 → Ctrl+C 丢整段 | `internal/live/live.go:124`、`:158-161`、`:140/:164/:237` | **本会话抽验确认**；另发现已写字节数 `n` 在该路径被直接丢弃 |
| 7 | **畸形响应直接 panic 崩进程**（E#1） | 零字节 `device.wvd`、截断 wvd、垃圾 protobuf（`int(length)` 溢出绕过判界）、DRM 许可证解析无边界检查；**全仓 `recover()` 0 命中** | `internal/drm/device.go:43`、`:62`、`:66`；`internal/appapi/appapi.go:321-326`；`internal/drm/widevine.go:434/467/507` | **本会话抽验确认**：`device.go:43` 对零字节直接取 `data[0]`；`:62/:66` 用文件内 uint16 长度切片且无判界 |

### 4.3 P1 队列（用户可见行为不一致）

- **直播韧性整批未落地**（C）：合并产物大小校验（0.8 阈值 + staging）、`LiveRecordResult` 三态、`qn=30000` + 回落（`live.go:59` 仍 `qn=10000`）、FLV 分段尾裁剪、终结态区分（无 flv / 写盘失败不重试）、录制前加载本地凭据（`commands.go:124` 不调 `InitSessionAsync`）；`live.go:250` 用 `http.DefaultClient`（Timeout=0）且无 60s 读停滞看门狗；`reconnectLimit=3` 与"不限次退避重连"相悖。
- **serve 关停与持久化**（A）：任务 ctx 取自 `context.Background()`（`server.go:315`），关停只 `taskWG.Wait()`（`:178-190`）→ 在途任务不落 Cancelled、不落盘；固定 `.tmp` 名 + 每任务并发写盘（`:699`、`:333`）、`finishedTasks` 内存列表永不裁剪（`:426`、`:683-693`）、`ErrorMessage` 未脱敏路径（`:356`、`:390`）；认证失败限速与字典有界（RF-9/RF-74）、token 常量时间比较、`BBDOWN_SERVE_TOKEN` 环境变量优先、`--trusted-proxy`、`/add-task` 429 缺 `Retry-After`（RF-83）、`/get-tasks` 缺 `no-store/nosniff` 与查询限流、`ParsePageSelection` 缺累计上限（RF-81 半对齐）、413 退化为 400。
- **配置与文件名**（D）：`cliHasUrl` 误判未修（`internal/config/parser.go:88-94` 全量扫描 argv，`--aria2c-proxy http://…` / `--work-dir av123` 会压掉配置里的 URL，上游 RF-7 已修）；`internal/util/file.go:35-69` 无 `TrimEnd`，Windows 上 `video.` / `video ` 落盘失败，另缺 100 字符基名截断。
- **DRM 安全纵深**（E2）：取钥链全程无 context（`internal/drm/decryptor.go:13`、`widevine.go:246-256`、`workflow.go:855`）→ serve `/cancel` 与 Ctrl+C 在取钥窗口（最长约 6 分钟）无效（RF-35）；无密钥内存清零；临时 key 文件覆写长度 64/65 字节（`internal/drm/mp4decrypt.go:50`）。
- **解析层静默语义**（E3）：`internal/fetcher/bangumi.go:95`、`intl_bangumi.go:119` 无条件丢预告 ep 且未命中 ep 不报错 → `workflow.go:1279-1281` 回落 ALL **整季静默下载**；反向的 intl 空轨道（`internal/parser/parser.go:487-514` 无 code / play-limited 校验）在 `workflow.go:761/820` 记为成功并入档。
- **其余**（B/C）：多段 FLV 中间 `.ts` 无 `finally` 清理（`muxer.go:343-367`）；DOVI 探针无 5s 超时（`muxer.go:192-206`）；`--comments` 父目录不建；SubOnly 恒 `.srt`；无事务化 `.muxing` 临时产物（直写 `savePath`，半成品会被"存在即跳过"）；入档按分 P 而非 aid；VOD 读停滞无看门狗；cover-only 无提前 return（会下全量）；aria2c URL 未剔除换行；`sub check` 逐 aid 只 `LogWarn+continue`、末尾恒 `return nil`（`commands.go:437-447`，部分失败仍以退出码 0 结束）；article/live 不读 `--work-dir` 且未过保留名净化；登录 `code==0` 与空 `access_token` 校验缺失（`login.go:104`、`:221-226`）。

### 4.4 结构性结论

1. **上游最大的主题在 Go 侧天然消失。** 「异常类型穿透两级失败隔离过滤器」一族（RF-14/31/43/44/47/49/52/64/72…）源于 C# 异常体系；Go 用 error 返回值 + 页面级 `bool`，**无该逃逸面** —— B、E 两片独立得出同一结论，共 10+ 条判 ✅。这是移植量最大的净减项。
   但**唯一逃逸通道是 panic，且全仓 `recover()` 为 0** → 收口 panic 边界即可整族关闭（见 P0#7）。
2. **三个横切设施谁修都得先有**，否则补一条漏一条：
   - **日志净化**：全仓只有 `internal/workflow/workflow.go:970` 的 `maskSecret`（仅 DRM 密钥），无 `SanitizeLogString`/`SanitizeServerText`/控制字符剥离 → RF-54/RF-70 无落点，A/C/D/E 四片都会被触发。
   - **路径段净化**：无 `PathUtil.SanitizePathSegment` 等价物（P0#3 的收口点）。
   - **响应体上限**：5 处裸 `io.ReadAll`，无 64MB / gzip 48MB 上限（RF-28/51/79）。
3. **测试基座为零。** `internal/` 下 0 处 `httptest`、0 个 `testdata`；19 个测试文件全是纯函数单测。上游的 `FakeBilibiliApiServer` + `Fixtures/parser/*.json` 对 Go 仓库**不是移植而是从零建**。E 已把上游 18 个夹具逐条标注 Go 预期红/绿，其中 `drm-dash-badkid`、`dash-reparse-pass1/2`、`durl-replay-first/empty` 四项**当前必红**。
   → **建议建基座排在补具体行为之前。**
4. **DRM 子系统是全仓最薄弱环节**：无取消贯通、无密钥零化、临时 key 文件覆写长度不足、许可证解析无边界检查。

### 4.5 待决策

| # | 事项 | 选项 |
|---|---|---|
| 1 | 免二压重发机制**整体缺失**（E：基线 v1.6.9 之前的缺口，上游本轮 4 条精化无对应物） | 补齐 / 登记为有意降级并在 README 声明 |
| 2 | ❓ RF-45（免二压重发降级丢杜比音轨） | 与上一条合并定案（B 已判 Go 无该链路） |
| 3 | 版本号策略 | D4：`<上游版本>-go`（如 `1.6.19-go`） |
| 4 | 协作姿态 | D5：引入 `CHANGELOG.md` + `AGENTS.md` + 格式硬门禁 |
| 5 | 修复批次划分 | 建议顺序：P0 → 横切设施 → 测试基座 → P1 分批 |

### 4.6 修复记录

| 批次 | 条目 | 改动 | 回归测试 | 变异验证 |
|---|---|---|---|---|
| 1 | **P0#1 混流输出选项位置** | `internal/muxer/muxer.go`：`-metadata:s:*` / `-disposition` / `-map_chapters` 从「紧跟各自 `-i`」改为累积到 `outputOpts`，等全部输入就位后统一落位 | 新增 `internal/muxer/muxer_test.go`（该包此前**零测试**）：参数顺序断言 + 真实 ffmpeg 端到端（封面+章节+字幕） | 撤掉修复后两条全红，端到端复现 `exit status 234`；恢复后全绿 |
| 1 | **P0#7 畸形响应 panic** | `internal/drm/device.go`：空文件守卫 + `parseWvd` 两处长度判界；`internal/drm/widevine.go`：新增 `readDelimited` 有界读取，三处解析器改用它，并补 `n==0` 守卫与未知 wire type 的 `default` 退出；`internal/appapi/appapi.go`：`walkFields` 长度改按 `uint64` 比较（原 `int(length)` 溢出可绕过判界）+ varint 守卫 | 新增 `internal/drm/parsers_test.go`、扩展 `internal/appapi/appapi_test.go` | 撤掉修复后 panic：`index out of range [0] with length 0`、`slice bounds out of range [11:10]`；恢复后全绿 |

> 顺带修掉一个未登记的缺陷：`parseLicenseKeys` / `parseKeyContainer` 在 wire type 为 1/3/5 时 **`pos` 不推进 → 死循环**（同一批畸形输入即可触发挂死，比 panic 更隐蔽）。已加 `default` 分支退出。

### 4.7 第二轮修复（P0#2 – P0#6）

| 条目 | 改动 | 回归测试 | 变异验证 |
|---|---|---|---|
| **P0#2 mp4box 丢配音/背景轨** | `muxer.go`：`muxByMp4box` 增 `audioMaterial` 形参，配音轨进入 `-add` 链并以 `-udta` 命名（与上游 v1.6.17 逐行对齐）；`workflow.go`：背景轨只在**确实混流**的分支删除，`--skip-mux` 下改为 `OnSaved` 上报 | `internal/muxer/muxer_test.go` 新增 mp4box 参数断言（假 mp4box） | 去掉该循环 → `the dubbing track never reached the mp4box -add chain` |
| **P0#3 占位符与 id 路径穿越** | 新增 `util.SanitizePathSegment`（合法值恒等；分隔符/控制字符/`.`/`..` 收口，复用 `GetValidFileName` 的保留名防护）；接入 13 处：`downloader.go` 的 `<aid>/<cid>/<ownerMid>/<dfn>/<videoCodecs>/<res>/<fps>/<audioCodecs>`，`subtitle.go` 的 5 处字幕 `lan`/`langKey`/`key` | `internal/util/sanitize_test.go` | 放开 `..` 的快速路径 → 测试立即红 |
| **P0#4 serve 读/写门禁** | `server.go`：新增 `guardMiddleware` 并**无条件安装**（原先无 token 时中间件根本不挂）；Host 校验挡 DNS rebinding（仅回环监听生效，不影响 LAN + token 部署）；写端点要求同源 Origin 与 `application/json`；补 `no-store`/`nosniff` 与 429 的 `Retry-After`；抽出 `buildHandler()` 让「门禁是否真的挂上」可测 | `internal/server/guard_test.go`（3 条） | — |
| **P0#5 携凭据 3xx 外发** | `util/http.go`：新增 `IsTrustedCookieHost` 与 `credentialRedirectGuard`，共享 client 只允许跳到 B 站系域名，其余拒绝并报错（`DownloadClient` 有意不设：媒体 URL 合法跳 CDN） | `internal/util/http_test.go`（含真实 httptest 跳转的泄露断言） | 去掉 `CheckRedirect` → 两条测试全红 |
| **P0#6 直播录制丢内容 / 挂死** | `live.go`：`defer os.RemoveAll(segRoot)` 改为只清本会话目录、`.segs` 根仅在空时删除，失败时保留分段（三处日志不再说谎）；读中断不再先删分段，已写字节计入合成；新增 60s 读停滞看门狗与无限指数退避重连（3s→30s）；本地写盘失败经 `errLiveWrite` 归为终结态；留 `resolveLive` 测试缝 | `internal/live/stream_test.go`（5 条） | 恢复「先删段再判取消」→ `recorded bytes were discarded`；禁掉看门狗 → 挂 5s 报 `the watchdog did not fire` |

> **P0 队列已全部关闭（7/7）。** 新增 6 个测试文件（`muxer` 与 `drm` 两个包此前零测试），全量 14 个包通过；每条修复都以「撤掉修复必须让测试变红」验证过，避免留下假绿的回归网。

> **仍未处理**：§4.3 的 P1 队列（约 30 条）与 §4.5 的 5 项待决策。

### 4.10 第五轮：夹具基座扩容

上游 15 个 parser 夹具已全部落地到 \`internal/parser/testdata/\`，回放基座覆盖其中 7 个：

- 已接（绿）：\`drm-dash-badkid\`、\`drm-dash\`、\`biz-error\`、\`missing-nodes-tolerant\`、\`dolby-flac-audio\`（1 视频 / 4 音轨 — **DoVi + FLAC 追加在 Go 侧本来就正确**，此前只是静态推断，现有夹具背书）。
- **已接（跳过）**：\`TestFixtureReparseProtocol\` —— 免二压重发协议（qn=0 首请求 → qn=127 重发 → 失败回退首次响应）在 Go 侧**完全不存在**，单次请求。断言已写好，只等实现后删掉 \`t.Skip\`。
- 依赖重发协议、暂不可接：\`dash-reparse-pass1/2\`、\`durl-replay-first/empty\`、\`flv-durl\`（期望 2 次请求、第二次带 \`qn=127\`）。
- 需按 query 分流的假服务器：\`intl-code0/1\`、\`bangumi-web-dash-*\`。

> 该测试即 §4.5 待决策 1 的**具体形态**：决策不再是纸面讨论，而是一个断言写好的跳启用例。

### 4.11 第六轮：DRM 取钥链贯通 context

| 条目 | 改动 | 验证 |
|---|---|---|
| **E2 DRM 取钥不可取消**（上游 RF-35）+ RF-4 + RF-79 | 取钥链是线性的 `GetKeyWidevine → GetKeys → getKeysInternal → sendRequest`，一次贯通：`sendRequest` 改 `http.NewRequestWithContext`；`http.DefaultClient` 换成 `licenseClient`（**禁跟随重定向** —— 请求体是从设备密钥签出的 challenge，不能跳到别的主机，上游 RF-4）；响应体加 64MB 上限（RF-79）；`workflow.go` 调用点包 `context.WithTimeout(ctx, 2*time.Minute)` 给出整体上限 | 取钥窗口（最长约 6 分钟）现在随 ctx 取消 —— serve `/cancel` 与 Ctrl+C 生效；15 个包全绿 |

> 至此取钥链的三个问题（无取消 / 跟随重定向 / 无上限）一并收口。**剩余**：§4.3 的 P1 队列其余条目（serve 关停取消、解析层静默整季下载、直播韧性与 `qn=30000`、`sub check` 失败计数等）与 §4.5 的 5 项待决策。

### 4.9 第四轮：P1 起步

| 条目 | 改动 | 验证 |
|---|---|---|
| **D#2 配置合并 `cliHasUrl` 误判**（上游 RF-7，v1.6.14 已修） | `internal/config/parser.go`：URL 启发式原先**全量扫描 argv**，`--aria2c-proxy http://127.0.0.1:1080`、`--work-dir av123` 这类**选项值**会被当成目标 URL，进而丢弃 `BBDown.config` 里的真实 URL，表现为「缺少参数」。新增 `positionalTokens`（复用已有的 `aliasMap`/`boolFlags` 判断选项是否吃值，与 `IsSubCommandInvocation` 同一套规则），只对**位置参数**做 URL 判定 | 新增用例先红后绿：修复前合并结果把配置 URL 丢掉并留下悬空的 `--encoding-priority`；修复后保留配置 URL、且命令行真给出目标时仍然压制配置 |

> **下一步（明确起点）**：A 方案 —— 把剩余 9 个上游夹具（`dash-reparse-pass1/2`、`durl-replay-first/empty`、`dolby-flac-audio`、`intl-code0/1`、`bangumi-*`）接上同一个回放基座。其中 `dash-reparse-pass1/2` 正是「免二压重发机制整体缺失」那条待决策，接上后决策就从纸面讨论变成一个有红测试支撑的具体选择。因此**该做在 DRM 取钥传 context 之前**：基座就位后，后续 P1 每条都能顺手带一条回归用例。

### 4.8 第三轮：横切设施与测试基座

| 项 | 改动 | 验证 |
|---|---|---|
| **日志净化设施** | `internal/util/logger.go`：新增 `SanitizeLogString`（控制字符 → 空格、按 rune 截断到 512）与 `sanitizeLogArgs`，**在 sink 处按参数净化** 6 个日志方法（`Printf` 交互提示保持原样）。一次覆盖 `util.Log*` 的全部调用点，C/A/D/E 四片点名的 RF-54 / RF-70 从此有落点 | 新增 `internal/util/logsanitize_test.go`；变异后伪造的 `[伪造] 我是一整行假日志` 真的成为独立日志行、`\x1b[0m` 打到终端 |
| **parser 夹具基座** | 落地上游 6 个 `Fixtures/parser/*.json` 到 `internal/parser/testdata/`；新增 `fixture_test.go`：`httptest` TLS 假服务器 + `config.Host` 指向它 + `skipSSL` 客户端，把上游 `ParserFixtureTests` 的回放方式移植过来。**`internal/parser` 首次有测试** | 4 条用例（badkid / 正常 kid / 业务错误 / 缺节点容错），为后续把 15 个夹具全部接上留下了基座 |
| **顺带修掉两个偏差** | ① UGC playurl **硬编码 `api.bilibili.com`**，导致 `--host`（镜像站）在普通视频路径上失效；改为用 `Cfg.Host` 并新增 `apiBase`（上游 `WithApiScheme` 语义，允许 host 自带 scheme）—— 这同时也是夹具注入的前提。② `bilidrm_uri` 原先取 `//` 之后的**全部内容**当作 kid，畸形 URI 会把 `evil.example/path?x=1` 存成 key id；现仅接受 32 位 hex（`parseKidFromDrmURI`） | 变异回旧解析 → badkid 用例立即红 |

## 5. 已知缺口（对账前采样，待各片报告确认）

| 上游加固项 | Go 现状 | 初判 |
|---|---|---|
| Windows 保留名（CON/NUL/COM1） | `internal/util/file.go:16` `reservedNames` | ✅ 已对齐 |
| 文件名净化 | `internal/util/file.go:35` `GetValidFileName`、`internal/live/live.go:287` `SanitizeFileName` | ✅ 已对齐 |
| 429 补 `Retry-After`（RF-83） | `internal/server/server.go:303` 有 429，全仓库无 `Retry-After` | ❌ 缺失 |
| API 响应体 64MB 有界读取（RF-28/51/79） | `io.ReadAll` 5 处，无 `LimitReader` | ❌ 缺失 |
| 携凭据 GET 逐跳可信校验（RF-50） | `util/http.go` 的 client 未设 `CheckRedirect`，依赖 Go 标准库跨域剥离 Cookie 的默认行为 | ⚠️ 不等价 |

## 6. 决策记录

| # | 决策 | 结论 | 日期 |
|---|---|---|---|
| D1 | 参照源如何维护 | 新增 `upstream` remote（aliveranme/BBDown），保留 `origin/master` 作为本项目 C# 快照分支；对齐以 `upstream` 为准 | 2026-09-19 |
| D2 | 是否快进 `origin/master` | 暂不推送远端。本地以 `upstream/master` + tags 作参照即可满足对账；是否把 C# 快照推到 v1.6.19 待定 | 2026-09-19 |
| D3 | 对齐目标版本 | 目标 **v1.6.19**（8 个 release / 111 提交） | 2026-09-19 |
| D4 | Go 版本号策略 | **建议 <上游版本>-go**（如 `1.6.19-go`）：版本号即「已对齐到哪一版」的声明，一处可见、可被脚本校验。待补完 1.6.12+ 行为后落地，届时同步改三处硬编码与 `CheckUpdateAsync` 的 tag | 2026-09-19 |
| D5 | 是否引入 CHANGELOG / AGENTS.md 等协作姿态 | **建议引入**：Go 仓库当前无 CHANGELOG、无协作规范、CI 仅 build/vet/test/gofmt；上游有 Keep-a-Changelog + AGENTS.md + 格式硬门禁，可作为模板 | 2026-09-19 |

## 7. 维护约定

- 每次上游发版后重跑 §3 步骤，把新增条目追加到对应片的明细表。
- 判定必须带 Go 侧 `文件:行号` 证据；给不出证据就标 ❓，不要凭印象写 ✅。
- 本文件是唯一基线，不另建平行清单。
