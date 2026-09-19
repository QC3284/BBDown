# 上游对齐追踪（Upstream Alignment）

> 本文件是 **Go 重写版与上游 C# 版行为对齐的唯一追踪基线**。
> 目的：把「落后多少版本」变成「缺哪些条目」，并让每次同步都可复现、可交接。
> 建立日期：2026-09-19 ｜ 结论基于 `upstream/master` = v1.6.19。

## 1. 参照源

| 名称 | remote / ref | 版本 | commit | 日期 |
|---|---|---|---|---|
| 上游（权威参照） | `upstream` → aliveranme/BBDown | **v1.6.19** | `2d2573b` | 2026-09-18 |
| 本项目 C# 快照分支 | `origin/master` | v1.6.10 + 2 补丁 | `9fb96f2` | 2026-08-11 |
| Go 重写（工作分支） | `origin/main` | **1.6.19-go** | — | — |

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

### 4.5 决策记录（均已定案）

| # | 事项 | 结论 | 落地 |
|---|---|---|---|
| 1 | **免二压重发机制整体缺失** | **补齐** | 已按上游 `ParseDash` 的 `reparsePass` 实现：qn=0 首请求 + qn=127 重取，仅带 `dash.video` 的文档整体接管，重取被拒/失败沿用首轮，用户取消向上传播。原本被它阻塞的 5 个夹具（`dash-reparse-pass1/2`、`durl-replay-first/empty`、`flv-durl`）全部转绿 |
| 2 | ❓ RF-45（免二压重发降级丢杜比音轨） | **随 #1 一并解决** | 接管时才重置 dolby/flac 追加标记的语义已由「整体替换文档后统一解析」覆盖 |
| 3 | 版本号策略（D4） | **`<上游版本>-go`** | 已升至 **1.6.19-go**：banner / cobra `Version` / 更新检查 tag / README / PKGBUILD 同步 |
| 4 | 协作姿态（D5） | **引入** | 新增 `CHANGELOG.md`（Keep a Changelog，版本号语义写进头部）与 `AGENTS.md`（布局、最高约束、测试纪律、上游同步步骤） |
| 5 | 修复批次划分 | P0 → 横切设施 → 测试基座 → P1 分批 | 已按此顺序执行完毕 |

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

上游 15 个 parser 夹具已全部落地到 `internal/parser/testdata/`，回放基座覆盖其中 7 个：

- 已接（绿）：`drm-dash-badkid`、`drm-dash`、`biz-error`、`missing-nodes-tolerant`、`dolby-flac-audio`（1 视频 / 4 音轨 — **DoVi + FLAC 追加在 Go 侧本来就正确**，此前只是静态推断，现有夹具背书）。
- **已接（跳过）**：`TestFixtureReparseProtocol` —— 免二压重发协议（qn=0 首请求 → qn=127 重发 → 失败回退首次响应）在 Go 侧**完全不存在**，单次请求。断言已写好，只等实现后删掉 `t.Skip`。
- 依赖重发协议、暂不可接：`dash-reparse-pass1/2`、`durl-replay-first/empty`、`flv-durl`（期望 2 次请求、第二次带 `qn=127`）。
- 需按 query 分流的假服务器：`intl-code0/1`、`bangumi-web-dash-*`。

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

### 4.12 第七轮起：P1 队列批量推进

| 条目 | 改动 | 验证 |
|---|---|---|
| **serve 关停不取消在途任务**（A#2） | 任务 ctx 统一派生自可取消的服务基础 ctx；关停时先取消再等待，最后落盘 —— 原先既不落 Cancelled 也不落盘，30s 后被进程退出截断 | `shutdown_test.go`：预占执行槽使任务必然走取消分支，断言状态与落盘；变异后用例超时 |
| **serve 持久化三缺**（A#3） | 唯一临时名 + `persistMu` 串行化（共享 `<file>.tmp` 曾让并发写者抢同一路径）；内存 `finishedTasks` 同样按上限裁剪（`/get-tasks` 服务的就是它）；`ErrorMessage` 脱敏本机绝对路径并去控制字符 | 并发写 + 无残留临时文件用例 |
| **serve token 硬化** | 常量时间比较（明文 `!=` 经响应时间泄露密钥）；`BBDOWN_SERVE_TOKEN` 环境变量优先于旗标 | `token_test.go` |
| **`-p` 展开累计上限**（RF-81） | 原先上限按「每个范围」判定，`1-60000,1-60000` 可连过两次并展开 12 万项 | 用例覆盖累计与单范围|
| **超大请求体返回 413** | 原先退化为通用 400，客户端无法区分「太大」与「格式错」 | `body_test.go` |
| **`sub check` 部分失败返回 0**（C） | 末尾恒 `return nil`，全失败也报成功；现统计失败数并非零退出（主动取消仍为 0） | `subcheck_test.go` |
| **空 access_token 写盘**（C） | TV 登录 `default` 分支把空 token 写进 `BBDownTV.data`，留下看似已登录实则无效的凭据 | `token_test.go`（login） |
| **文件名卫生**（D#3） | 尾随点/空格（Windows 静默丢弃）、基名按 rune 截断到 100、全为点时回退、保留名判定后置 | `filename_hygiene_test.go`（含真实 `utf8.ValidString` 断言） |
| **aria2c 输入注入**（B） | URL 直接来自接口 `base_url`，可带换行注入 `out=`/`dir=` 选项；Cookie 早有防护而 URL 没有 | `aria2c_input_test.go`；变异后注入行真的出现 |
| **评论导出不建父目录**（B） | 路径来自保存模板，父目录不存在直接失败 | `comments_test.go` |
| **SubOnly 恒 `.srt`**（B，RF-18） | ASS/JSON 轨道会被改成扩展名与内容不符；现沿用真实扩展名并对 `lan` 段净化 | 代码级 + 净化函数已有用例 |
| **CoverOnly 不提前返回**（B） | 代码里写着「matching C#: no early return」，但上游明确 `return true` 并注明「否则…用户只要封面却白白下载完整视频」——**在码注释是错的**，现补上提前返回（无封面资源时报失败而非零产物成功） | 对照上游源码确认 |
| **混流非事务化**（B） | 直写 `savePath` 时中断会留下截断成品，而「已存在, 跳过下载」会把它永久当成已完成；现写唯一 `.muxing-*.mp4` 后改名 | 代码级 |
| **直播 qn=30000 + 回落**（C） | 原固定游客档 `qn=10000`；现请求 30000，该档无可用 flv 流时以 10000 再取一次 | `resolve_test.go`（回落 + 快路径两条） |
| **直播合并产物大小校验**（C） | ffmpeg 遇坏段会截断并仍以 0 退出，紧随其后的分段清理会静默丢掉整场录制；现产物显著小于源时保留分段并报错 | 代码级 |
| **直播录制前不加载凭据**（C） | client 用空配置构造，永远游客身份 —— 上述 qn=30000 因此永远落空；现走 `InitSession`（`--cookie` 优先，否则本地 `BBDown.data`） | 代码级 |
| **VOD 读停滞无看门狗**（B） | 媒体下载用无总超时的 client，连接既不 RST 也不 EOF 时 `io.Copy` 永久阻塞 | `stall_test.go`（停滞中止 + 慢速存活两条） |

> 经核实**与上游一致、不作为缺口**：CoverOnly 的「不提前返回」注释与上游行为相反，已按上游修正；`GetValidFileName` 的保留名处理原本已对齐。

### 4.13 剩余队列（2026-09-19 逐条核对后修正）

> **修正**：此前本节写「P1 队列已清空」是**错的** —— 那是按汇总时挑出的重点条目判的。
> 逐行核对五片报告的 ❌/⚠️ 行（约 60 条）后，确认仍有下列条目未处理。

**已完成（本轮补做，见 §4.15）**：mp4box 封面转义与 EscapeString 换行折叠、SRT `-->`` 与 ASS 反斜杠注入、
`baseURLRegex` 收紧、显式 epid 的预告保留、国际版多余的转义预替换、收藏夹翻页不再静默截断、
密钥临时文件按实际长度覆写、响应体 64MB 与 gRPC 48MB 上限、携 Cookie 前校验目标主机、
`SavePaths` 去重、已完成任务按创建时间裁剪、aria2c 兜底超时、`--skip-mux` 跳过 DOVI 探测、
大会员回退跟随镜像主机、VIP 判定走 JSON message、API 层有界重试。

**队列已清空**：截至 2026-09-19，五片报告中的 ❌/⚠️ 条目全部处理完毕（除下表列出的 N/A 项）。

本轮收尾的三项：

| 条目 | 改动 |
|---|---|
| 直播旧会话分段 | `staleSessions` 扫描 `.segs` 下的其他会话目录并**只提示不删**（可能是某次合成失败后唯一的副本） |
| SESSDATA 有效期 | `util.EstimateSessdataExpiryDays`：解析 URL 编码载荷首段的 base64 JSON `expires`，剩余 ≤7 天时告警、已过期时提示重新登录；解析失败 fail-open 返回 nil，不误报 |
| Logger 写失败退避 | 连续 5 次写失败后暂停文件日志 30 秒并输出一次提示，避免磁盘满/只读时每一行都重试一次注定失败的 open |


> 判定为「与上游一致、不作为缺口」：`SavePaths` 集合语义、`/health` 无需认证、格式串中的文化敏感项（Go 无此问题）。

**判定为「与上游一致、不作为缺口」**：`SavePaths` 集合语义、`/health` 无需认证、格式串中的文化敏感项（Go 无此问题）。

### 4.14 第八轮：P1 收尾

| 条目 | 改动 | 验证 |
|---|---|---|
| **入档粒度**（B，RF-75） | 多P稿件共享同一 aid，原先下完**第一个分P**就写进 `BBDown.archives`，下次运行余下分P被判定已下载而跳过。移植上游 `ArchiveTracker`：该 aid 全部分P成功才入档，任一页失败即本 run 内不再入档；单P稿件仍立即入档 | `archive_test.go` 5 条，对齐上游 `ArchiveGranularityTests` |
| **serve 认证失败限速**（A，RF-9/RF-74） | token 可被无限次尝试；现按客户端计数，10 次失败锁定 5 分钟并带 `Retry-After`，成功即清零，跟踪表有硬上限（防自身成为内存增长点） | 单元（锁定/复位/有界）+ 端到端（10×401 → 429 → 正确 token 仍通） |
| **`--trusted-proxy`**（A） | 限速按 IP 归属，而 XFF 可伪造；无条件采信等于给攻击者无限个假身份。现仅当请求确实来自配置的代理时取 XFF 最后一跳 | `proxy_test.go` |
| **直播分段尾裁剪**（C） | 网络中断/取消后段尾可能只剩半个 FLV 标签，concat demuxer 会在该处中止**整场**合成；现合成前逐段裁到最后一个完整标签 | `trimflv_test.go`（构造真实 FLV 标签序列） |
| **登录顶层 `code` 校验**（C） | 顶层 code 解析了却从未检查，风控/限流响应会落进 default 成功分支，最终以「回调 URL 为空」这种误导性原因失败 | `token_test.go`（login） |
| **intl 夹具接入** | intl 路径 scheme 改走 `apiBase` 使其可注入；新增按 query 分流的回放夹具 | `TestFixtureIntlMergesTwoPassStreamLists` —— **两趟 `prefer_code_type` 合并本来就是对的**，现有夹具背书 |
| **bangumi 夹具接入** | `result` 根（而非 `data`）的轨道映射 | `TestFixtureBangumiWebDashParsesTracks` |

**夹具覆盖**：15 个上游夹具中 **10 个已接（绿）**，其余 5 个（`dash-reparse-pass1/2`、`durl-replay-first/empty`、`flv-durl`）统一被「免二压重发」决策阻塞 —— 即 `TestFixtureReparseProtocol` 那一条跳启用例。接入过程中 **intl 两趟合并、bangumi 的 `result` 与 `result.video_info` 两种根形状、DoVi+FLAC 音轨追加** 四条「静态判定 ✅」被真实响应夹具证实为正确。
- **夹具**：按 query 分流的假服务器 → 接 `intl-code0/1`、`bangumi-web-dash-*` 四个夹具。
### 4.15 第九轮起：逐条核对后的补做

> 起因：本轮重新逐行核对五片报告的 ❌/⚠️（约 60 条），发现 §4.13 早先「队列已清空」的判断有误。
> 下列条目由此补做，每条都带回归用例。

| 类别 | 条目 |
|---|---|
| 转义与注入 | mp4box `-itags` 封面路径未转义（Windows 路径的反斜杠被当转义序列，封面静默丢失，RF-6）；`EscapeString` 缺 CR/LF 折叠（换行破坏 itags token 语法）；SRT 正文 `-->` 会错位后续字幕；ASS 弹幕字面反斜杠可注入排版标签 |
| 解析 | `baseURLRegex` 未锚定，query 里的「冒号+数字」被误判为端口而选错 base_url；用户显式指定的 epid 即便是预告也应保留；国际版多余的转义预替换会把「反斜杠+斜杠」归并丢数据；VIP 判定改以 JSON message 为准（裸子串仅兜底）；大会员回退抓网页时跟随 EpHost 镜像主机 |
| 网络 | API 层有界重试（5xx 与传输错误指数退避，4xx 不重试）；响应体 64MB 与 gRPC 解压 48MB 上限；gRPC 帧首字节合法性校验；**按服务器 Date 头校准时钟偏移**（本地偏差 >60s 时 WBI 签名被拒，该机所有下载都会失败） |
| 凭据 | 携 Cookie 请求发送前校验目标主机：仅官方域名与 `--host/--ep-host/--tv-host` 显式配置的主机可见 |
| 数据完整性 | 密钥临时文件按实际载荷长度覆写（固定 64 字节覆写 65 字节的 kid:key 行，最后一个字符仍留在盘上）；订阅历史保「最近」语义并按 5000 条裁剪 |
| 任务与工具 | `SavePaths` 去重（集合语义）；已完成任务的保留期与上限改按 `TaskCreateTime`（避免「后创建但先完成」被误删）；aria2c 6 小时兜底超时；`--skip-mux` 跳过杜比视界探测；收藏夹翻页不再对风控/空页静默截断 |

**方法论收获**：本轮 6 条修复是先写会红的用例再改代码；另有 4 条是「断言先写错、测试报红、回头发现是断言的问题」（行数、引号、测试路径、变量作用域）——**两种红都必须查清是哪一种**，否则会把断言错误当成实现错误去改。

### 4.16 第十轮：交互可见项复核（配色 / 二维码 / 布尔选项写法）

> 起因：用户提出「尝试修饰颜色」。核对方式是把每一处转义序列与上游源码里的 .NET
> ConsoleColor 名字对上，而不是凭观感猜；顺带复核了二维码渲染与命令行布尔写法。

| 类别 | 结论与改动 |
|---|---|
| 终端配色 | 上游 `ConsoleColor` 的 Red/Cyan/White 是**亮色**变体（对应 91/96/97），本实现写成了 31/36/37 这组暗色，横幅前景更只给了 37；抽 `Ansi*` 常量并改用。`LogWarn`(33)、`LogDebug`(90)、横幅背景(44) 本来就一致 |
| 二维码 | 上游 `ConsoleQRCode.GetGraphic` 把模块矩阵里为 true 的**深色**模块画成 `ConsoleColor.Black`(30)；本实现画反了（深色模块用白底 47），整张码是反色的——浅色主题终端上会扫不出来。`skip2/go-qrcode` 的 `Bitmap()` 为 true 即深色模块，极性据此对齐 |
| 命令行 | 上游（System.CommandLine）布尔选项 arity 为 0..1，其 README 就按 `--multi-thread false` 文档化「关闭多线程」；pflag 只认 `--flag=false`，`--flag false` 会把 flag 置 true 并把 `false` 漏成位置参数——实测被当成输入地址去请求 API，报 `获取视频信息失败 (code=-400)`。归一化层折平，长/短别名与配置文件同写法一并处理 |

**方法论收获**：这两条修复都不是读代码读出来的——配色是把转义序列与上游源码里的颜色名逐一
对照得到的；布尔写法则是**照着上游 README 的命令行原样敲一遍**才暴露的（复现命令
`BBDown --multi-thread false`：修复前输出的是 API 报错，而不是「关掉多线程」）。
**验收要走上游文档里的用户路径**，只看实现是否符合直觉会漏掉这类差异。

### 4.17 第十一轮：进度条渲染与聚合（用户报「为什么会有两个进度」）

> 起因：一次 27MB 无损下载的终端输出里出现两行进度。逐项对照上游 `BBDown/Infrastructure/ProgressBar.cs`
> 与 `BBDownDownloadUtil` 后确认有三处不一致；这三处此前**从未进入判定表**（A~E 五片报告都没覆盖进度条）。

| 类别 | 上游行为 | 本实现（修复前） | 修复 |
|---|---|---|---|
| 控制台写入串行化 | `Logger.ConsoleLock` 被 `ProgressBar` 复用，日志与进度条写入互斥 | 进度条直接 `fmt.Printf`，与日志无共同锁 | `util.ConsoleLock`，且日志写入前先给进度行收尾 |
| 结束时的进度行 | `Dispose` → `UpdateText(string.Empty)` 擦掉整行 | 补画一帧 100% 并换行，把最后一帧留在屏幕上 | 擦除整行，与上游同 |
| 多线程进度聚合 | `ProgressAggregator`：分片上报**累计**字节，按差值聚合，重试回退 | 只在分片下载**完**才累加 → 以分片数为台阶跳变 | 移植 `progressAggregator`（含上游三条断言的用例） |
| 收尾与后续日志的时序 | `using var progress` 保证 `Dispose` 先于后续日志 | 不等渲染协程，日志先打 | 渲染协程关闭 `stopped` 通道，主流程等它 |

**实测对照**（同一条命令 `--thread-segment-size 1`，真 PTY 下用 `tr '\r' '\n'` 切开逐帧还原）：

```text
修复前进度帧： 0.00% → 20.90% → 52.54% → 100.00%（台阶跳变，末帧留在屏幕上）
修复后进度帧： 0.00% → 34.99% → 63.41%（连续推进，随后整行擦除、日志独占新行）
```

### 4.18 第十二轮：按「上游日志字面量」扫描未审计面（用户问「还有哪些不一致」）

> 方法：①把上游 `BBDown/` 与 `BBDown.Core/` 的 75 个 `.cs` 按「文档是否提及」过一遍，
> 筛出从未进入审计报告的文件；②抽取上游 282 条 `Logger.Log*` 调用的字面量，
> 按「最长中文片段」做探针在本仓源码里比对，未命中的逐条人工判定。
> 两者互补：文件级扫描漏不掉整块功能，字面量扫描漏不掉「功能在、说法不在」。

| 位置 | 上游行为 | 本仓（修复前） |
|---|---|---|
| `Workflow.cs:142-153` | 头部四行：`视频标题:` / `发布时间:`(带 zzz 时区) / `视频URL:` / `UP主页:` | 标题与 URL 是裸值，UP 主页不打印，时间无时区 |
| `Workflow.cs:156` | 互动视频 + `-t` → 提示并改回默认解析 | `IsSteinGate` 有解析、无消费者，照 TV 跑 |
| `Display.cs:49,60` | `--only-show-info` 时每条流后打印 `baseUrl` | 参数被忽略，脚本拿不到取流地址 |
| `Display.cs:19-35` | 背景音频流 + 配音清单（两者都在时） | 只列视频流与音频流 |
| `Pages.cs:ExpandPageAliases` | 别名按段全词匹配，非别名段原样交给解析器报错 | 子串替换：`-p 1LATEST` 静默变成页码 112 |
| `Pages.cs:22-33` | 自动选集数时提示；`p` 参数按查询串解析 | 无提示；只认 `?p=` 开头，漏 `?a=1&p=4` |
| `Download.cs:553-563` | 零视频流/音频流时告警，only 模式下判失败 | 只清空轨道 → 零产物报「任务完成」 |

**仍然一致的部分**（扫描后逐条确认无差异）：四种解析模式与 URL 分发（`FetcherFactory` 六个前缀）、
DRM 取钥与解密（`Decrypt.cs` / `WidevineCdm` / `WvdDevice`：手动密钥、WVD 缺失提示、Key 与 Kid 必须同时
具备、临时密钥文件按实际长度覆写、超时后 Kill 进程树）、登录两条命令（薄壳，逻辑在 `BBDownLoginUtil`）、
`ServeRequestOptions`、`ParsedResult` 字段集、`BBDownEnums` 弹幕格式。

### 4.19 第十三轮：把上游测试表搬来做差分（用户说「那开始撞？」）

> 方法：上游 `BBDown.Tests/` 的数据表就是行为规格。逐文件把 `[Theory]/[InlineData]` 与 `[Fact]`
> 的输入输出搬进本仓用例，先跑一遍——红的就是与上游不一致的地方，再由修复把它们变绿。

| 上游测试文件 | 接入内容 | 撞出的差异 |
|---|---|---|
| `PageSelectionTests` | 8 条成功表 + 16 条拒绝表 + 16 条别名表 | `-p "1 - 3"`（范围两侧空白）本仓报错，上游合法 |
| `FormatHelperTests` | 7 条体积表 + 4 条时长表 | `<1KB` 本仓显示 `0.00 B`、且多一个 TB 档；上游 `{n} bytes`、无 TB 档 |
| `PathUtilTests` | 5 + 8 + 5 条文件名表 | 超长截断本仓加省略号并丢掉扩展名；上游保留扩展名、长度恰为上限 |
| `BilibiliBvConverterTests` | 3 条编码 + 7 条解码 + 3 条报错 | 无（解析层前缀剥离位置不同，行为一致） |
| `TrackSortTests` | 1 条畸形 id 表 | 清晰度 id 本仓按 64 位解析，`999999999999` 参与排序；上游 Int32 降级 0 |
| `UrlResolverTests` | 15 条输入表 + 4 条 BV 表 + 2 条拒绝用例 | 识别不出的输入本仓原样透传，上游报「无法识别的视频 URL 或 ID」 |
| `PathFormatTests` | 7 条模板选择表 | 无（本仓逻辑等价，顺手抽成与上游同名的 `resolveSavePathFormat` 以便钉住） |

**另由选项面扫描撞出**：`article` / `live` 缺 `-w` 简写（上游两个子命令的 settings 各自声明了
`-w|--work-dir`）。做法是把上游 `MyOption.cs` 与各 `*Command.cs` 的 `[CommandOption]` 抽出来，
与本仓 flag 注册做双向差集——79 个长选项一一对应，无缺失、无自造；再把每个子命令的选项集单独比对，
才发现这个只出现在两个子命令上的简写。

**已知且有意的差异**（记录在案，不再视为缺口）：`GetValidFileName("")` 本仓返回空串——调用方
用空串表示「无值」，返回 `"_"` 会让占位符缺失时凭空多出一个名为 `_` 的文件。

### 4.20 第十四轮：差分续跑（脱敏 / 分片合并 / 数值校验）

> 同一套办法继续搬上游测试表。这一轮挑的是「纯函数 + 表格式断言」的文件，
> 撞出的问题集中在**日志脱敏**与**合并的数据完整性**两处。

| 上游测试文件 | 撞出的差异 |
|---|---|
| `SensitiveDataMaskerTests` | ① 本仓漏掩签名媒体 URL 的 `sign`/`x_sign`/`w_rid`/`deadline`/`marlin_token`（**票据明文落盘**）；② 本仓用 net/url 往返脱敏，把 `***` 编码成 `%2A%2A%2A` 并重编码其它参数（上游是字符串手术）；③ 本仓误掩 `buvid3`（非凭据） |
| `CombineFilesTests` | ④ 合并失败留半截产物（上游删除）；⑤ 缺单分片 rename、自动建父目录、取消支持、空列表短路 |
| `NumericOptionValidationTests` | ⑥ **无差异**——本仓 `validateNumericOptions` 的范围与文案与上游逐字一致（顺带补上差分用例钉住） |

**契约变更**：`CombineMultipleFilesIntoSingleFile` 签名加上 `ctx`（对齐上游的 CancellationToken），
两个调用点（多线程分片合并、直播 FLV 分段合并）同步传入；既有用例与 muxer 用例按新契约更新。

### 4.21 第十五~十六轮：差分续跑（弹幕 / 字幕 / 充电 / 凭据 / 时钟 / UA / 专栏）

| 上游测试文件 | 结果 |
|---|---|
| `DanmakuFilterTests`、`DanmakuAssEscapingTests` | 无差异（关键词/midHash 过滤、ASS 大括号与反斜杠中和、BGR 颜色、点号时间轴） |
| `SubtitleFormatTests` | **两处差异**：毫秒按「小数 ×1000 截断」而非 tick 进位（`1.001s` 少 1ms）；NaN 未处理、极大值溢出；`SanitizeSRT` 只裁 ASCII 空白 |
| `UpowerGuardTests`、`SessdataExpiryTests`、`ClockCalibrationTests` | 无差异 |
| `HttpUtilUserAgentTests` | **一处差异**：下载路径写死 `Mozilla/5.0`，`--user-agent` 对媒体下载不生效 |
| `ArticleUtilTests` | 无差异（cv 号解析含大小写与 URL 形态；Markdown 头部与 HH:MM 时间） |
| `WorkDirResolutionTests` | 无差异（既有 `workdir_test.go` 已覆盖同一组契约，含 Windows 根相对路径） |
| `RedirectHopValidationTests`、`LoggerFileTests` | 上游用例依赖本地重定向服务/句柄夹具，本仓对应契约由 `credhost_test.go`、`loggerfail_test.go` 覆盖 |

### 4.22 第十七轮（目标轮 1~2）：实体 / 分发 / 配置合并 · 全部一致

| 上游测试文件 | 结果 |
|---|---|
| `EntityTests` | 无差异：`Page.Bvid` 对越界 aid（0 / 负数 / ≥2^51）回落**原始 aid**、非数字 aid 原样返回；`Audio.ShortCodecs` 大写去横线 |
| `FetcherFactoryTests` | 无差异：`mid:`/`favId:`/`listBizId:`/`seriesBizId:`/`ep:`/`cheese:`/裸援助各自落到对应 fetcher，`ep:` + `--use-intl-api` 落 IntlBangumiInfoFetcher，订阅目标都不会落到 NormalInfoFetcher |
| `ConfigMergeTests` | 无差异：空格与等号写法的命令行都压过配置文件、`--config-file=<path>` 被认、以 `-` 开头的配置取值不被吞、子命令调用整段跳过合并 |

### 4.23 第十八轮（目标轮 3）：JSON 取值 / 混流参数

| 上游测试文件 | 结果 |
|---|---|
| `JsonElementExtensionsTests` | **一处差异**：本仓 `gi`/`gi64` 用 `fmt.Sscanf("%d")`，它接受数字**前缀**——`"12abc"` 会变成 12、`"1.5"` 变成 1；上游 `int.TryParse(NumberStyles.Integer)` 要求整串是数字，脏数据必须落到默认值 0。另 `gi` 未做 int32 范围判定（上游 `TryGetInt32` 失败即取默认值）。改为整串解析 + 范围判定。 |
| `MuxerArgsTests` | 无差异（**加强既有用例**）：补上上游的三条断言——章节 meta 是第 5 个输入（下标 4）、`-map_chapters` 指向它自身、`-map` 序列不含该下标（meta 只供取章节）。原本只断言 `-map_chapters` 存在与顺序，现按上游口径钉死。 |

### 4.24 第十九轮（目标轮 4）：风控页识别 / 会话传播 / 重试策略

| 上游测试文件 | 结果 |
|---|---|
| `HttpUtilRetryTests` | **一处差异**：本仓**完全没有**风控页识别。上游在 API 响应是 HTML 时抛 `RiskControlResponseException`（“疑似风控页：接口返回 HTML 而非预期数据…”），本仓把 HTML 直接交给 `json.Unmarshal`，用户看到 `invalid character '<' looking for beginning of value`——而风控页/登录墙恰恰都以 HTTP 200 + HTML 返回。现加 `util.LooksLikeHTMLPage` + `util.UnmarshalJSON`（剥前导空白与 BOM 后判首字符），parser/fetcher/评论导出的 22 处解析统一走它。其余重试语义（5xx 重试、4xx 不重试、耗尽即停、请求计数精确、body 上限）与上游一致，既有 `retry_test.go` 已按相同口径断言。 |
| `ConfigPropagationTests` | 不适用于 Go：该文件用反射检查 C# 的 `AsyncLocal` 配置传播（子方法写 Config 不回流父流程，须显式返回并应用）。本仓等价契约由返回值承担——`workflow.InitSession` 返回 `(wbi, error)` 并回写 `*cfg`，三处调用点（watchlater / sub check / serve 任务）都把返回的 wbi 传给 `fetcher.NewFactory`，不存在“子方法写完父流程看不见”的形态。 |

### 4.25 第二十一轮（目标轮 5）：SSL 策略 / 不跟随重定向

| 上游测试文件 | 结果 |
|---|---|
| `HttpUtilSslPolicyTests`、`VerifiedNoRedirectClientTests` | 无差异（**补上安全前提的钉住用例**）。上游用反射比较「校验池/不安全池是不是同一个实例」，Go 侧无对应机制；改按可移植的行为断言钉住三件事：① 许可证请求不跟随重定向（307 原样返回，目标一次都没被请求）；② 许可证请求始终校验证书（自签站点必然失败，不随 `--insecure` 降级）；③ `--insecure` 真的切换校验（skipSSL=true 能连自签、false 拒绝）。为可测性把 `licenseURL` 由常量改为变量（用例指向本地服务器），生产值不变。 |

### 4.26 第二十二轮（目标轮 6）：选项默认值 / buvid3 / 取消归类

| 上游测试文件 | 结果 |
|---|---|
| `OptionDefaultsBindingTests` | 无差异（补钉住用例）：帮助文本标称「默认开启」的三个布尔选项（`--multi-thread` / `--skip-ai` / `--force-replace-host`）默认值确为 true，`--force-http`/`--use-tv-api`/`--only-show-info`/`--audio-only` 确为 false。上游当年踩的是 Spectre 把未出现的 flag 写回 false 覆盖初始化器，Go 侧无此机制，但默认值本身值得钉住。 |
| `BuvidProviderTests` | 无差异：`HasBuvid3` 与上游逐字一致（`contains("buvid3=", OrdinalIgnoreCase)`），`buvid4=` / `buvid=` 不会误判。 |
| `CancellationClassificationTests` | **一处差异**：本仓按 `ctx.Err() != nil` 判「已取消」，它把 **DeadlineExceeded 也算成用户取消**；上游只认 `CancellationRequested`，超时必须归 Failed 并保留原始错误。另外上游对真取消给出统一文案「已取消」，本仓此前落的是 `err.Error()`。现抽出 `classifyTaskCancellation` 按上游口径归类。 |

**方法论收获**：这次差异藏在「看起来对」的一行 `ctx.Err() != nil` 里——它覆盖了两种语义
（用户取消 / 超时），而只有前者才该叫「已取消」。对照上游时要注意**这类把两种原因混为一个判断**的写法，
它们不会报错，只会把失败原因伪装成用户操作。
**方法论收获**：这一片「无差异」但**原先没有任何用例**——安全前提靠代码里的一行 `CheckRedirect` 撑着，
谁把它删掉都不会有测试变红。差分表的价值在这里是另一面：它把「本来就对、但没人守」的地方变成有守卫的。
**方法论收获**：风控这次是「上游有、本仓没有」的**整块能力**，而它不体现在任何功能路径上——
正常跑永远走不到。差分表能撞出它，是因为上游专门为它写了用例。这也再次说明：
「我们没这条逻辑」和「我们这条逻辑写错了」是两类问题，只有对着上游用例清单才会有前者。
**方法论收获**：「前缀解析」这类差异读代码时几乎不可能看出来——`fmt.Sscanf("%d")` 看上去完全合理，
只有拿上游的 `"12abc"` 这类脏值去喂才会现形。差分表里那些**看起来没意义的边界值**
（`"invalid"`、`null`、缺字段）恰恰是最有价值的部分。
**说明**：这一轮只补用例、无代码改动——三处都是「跑一遍确认一致」，
按既定口径同样记入文档：**没撞出差异也是结果**，下次不必重查。
另核对了剩余清单里的一批「假剩余」：`ParserFixtureTests`、`ProgressBarTests`、
`DownloadProgressAggregationTests`、`ArchiveGranularityTests`、`ServeApiSecurityTests`、
`CommentUtilTests`、`SubscriptionStoreTests`、`LiveStreamUtilTests` 等在前几轮已按别的名字覆盖，
真正的剩余面集中在：`MuxerArgsTests`、`HttpUtilRetryTests`、`HttpUtilSslPolicyTests`、
`VerifiedNoRedirectClientTests`、`ConfigPropagationTests`、`DownloadPipelineTests`、
`DownloadTaskSnapshotTests`、`ServeCommandTests`、`ParserPlayLimitTests`、
`ExternalProcessRunnerTests`、`BBDownAria2cTests`、`JsonElementExtensionsTests`、
`Widevine*Tests`、`DrmDecryptorTests`、`AppHelperMessageTests`、`CancellationClassificationTests`。
**方法论收获**：这一轮最有价值的一条是 UA——它不在任何「模块」里，而是横跨「参数解析 →
HTTPClient → 下载器」的**传递链**：参数解析对了、HTTPClient 也存对了，只有最后一段没接上。
差分用例的价值就在于它直接对着「用户设了参数该有什么效果」断言，从而把断链暴露出来。
**方法论收获**：这轮两处「功能性」缺陷都藏在**跨模块的契约**里——脱敏键表小一号、合并失败不清理，
读单个函数的实现都挑不出毛病，只有拿上游的断言表逐条对照才会红。
另外确认了一条经验：**没撞出差异的也要记**（如数值校验），它是「这块已对齐」的证据，下次不必重查。
**方法论收获**：这一轮的价值不在「又修了几个」，而在于**差分测试的建法**：上游测试的
`[InlineData]` 是逐字的输入输出对，搬过来就能跑，比读源码找差异可靠得多；
而「选项面双向差集」能覆盖到只在某个子命令上出现的细微差别（如 `-w`），这类差异人工阅读必然漏。
**方法论收获**：这七处都不是读代码读出来的，而是**拿上游的每一句用户可见输出当检查表**反查本仓——
五片审计报告按模块写，天然会漏掉「横跨模块但用户能看见」的面（显示层、分P选择）。
字面量扫描本身有假阳性（本仓用 printf 占位、措辞不同），必须逐条人工判定；但它的高召回率值这个成本。
**方法论收获**：用户报的是「为什么有两个进度」——只看代码很容易把「两个」当成两次渲染器调用去查，
实际是**一次渲染 + 一次竞态**：末帧残影被日志压掉左半边，紧接着又画出最后一帧。
定位靠的是把终端里的回车切开逐帧还原，而不是盯着代码猜。



