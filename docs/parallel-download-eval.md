# 下载并行评估（`--concurrency` 二期）

> 任务：t23（评估任务，**不改产品代码**，只读分析 + 产出本文件）。
> 来由：`docs/ROADMAP.md:140`「**下载并行**（`--concurrency` 二期）：当前只并行检查阶段、下载串行——
> 并行下载会引入终端进度条/SSE 渲染竞态，需先评估渲染层互斥」。
> 结论可直接开工：见 §6 实现点清单（file:line + 改法 + 用例 + 工作量）与 §7 回归网。

---

## 0. 结论摘要（TL;DR）

1. **终端进度条层是「单行假设」**：每条下载各自持有一个 `progressLine`，用 `\r` 覆盖「当前那一行」
   （`internal/download/progressline.go:18-40`）。N 个下载并行 = N 个实例抢同一物理行，
   `util.ConsoleLock` 只能保证「单次写入不被撕裂」，不能分配行（`internal/util/logger.go:108,148-160`）。
2. **这个竞态今天就能复现**：serve 的执行闸门允许 N 个任务并行（`internal/server/server.go:227-275,1033-1067`），
   只要 `bbdown serve` 的 stdout 是终端（`isTerminalOut` 看 `os.Stdout`，`internal/download/downloader.go:1052-1058`），
   每个任务都会画自己的进度条。也就是说：这不是「将来并行下载才有的新问题」，是 serve 已有的观感缺陷，
   并行下载会把它带到 CLI。
3. **方案定案：选 B（并行时终端降级为边界日志，进度细节走 `--progress-json`/SSE）**，并保留
   「并发=1 时仍是今天的单行进度条」。理由：A（多行进度条）要引入槽位注册表 + 区域高度 + 日志下移 +
   宽度/高度降级 + 平台差异，成本 4~6 人日且长期风险最高的一类（终端被写乱）；B 的成本约 1 人日，
   失败模式只是「不好看」，回退是一个开关。**A 不废弃**，作为后续 UX 升级（§4.4 给了分档规则）。
4. 并行下载除渲染外还有 6 个前置项（§6 表 2）：进程级 `os.Chdir`、`muxer` 全局句柄、`-i` 交互输入、
   `--progress-json` 帧缺任务标识、批量汇总的 `firstErr` 数据竞争、pending 文件写入串行化。
   其中 `pathLocks`（`internal/workflow/workflow.go:2041-2073`）**已经**为「两个任务下同一个产物」准备好了。
5. 工作量：B 路线 **4.5~5.0 人日**（含用例与量化，口径与 §6 表 1/表 2 合计一致）；A 路线额外 **4~6 人日**。

---

## 1. 现状与共享状态盘点

### 1.1 今天已经并行的路径

| 路径 | 并发度 | 证据 |
|---|---|---|
| `serve` 多任务 | `--max-concurrent`（默认 3） | `internal/server/server.go:227-275`（semaphore / acceptLimiter），`server.go:1033-1067`（processTask 取槽），用例 `internal/server/concurrency_test.go:17-60`（峰值并发=3） |
| `sub check` 检查阶段 | `--concurrency`（1-8，默认 1） | `internal/cli/root.go:623` 帮助文案「检查阶段的并发订阅数(1-8)，下载仍按订阅顺序串行」；编排在 `internal/cli/subcheck_schedule.go` |
| CLI 批量（多目标 / `-p` 多分P） | **串行** | `internal/cli/urlsfile.go:62-78` `runTargets` 顺序 for 循环；`internal/workflow/workflow.go` 的 `Run` 逐页循环 |
| 单文件内部 | 多线程分片 | `DownloadConfig.MultiThread` + 自适应分片（ROADMAP O4 已完成） |

结论：**「任务级并行」在 serve 已经存在**，CLI 侧要补的就是它；渲染层的问题在 serve 上已经现实存在。

### 1.2 渲染层共享状态（并行下逐条失效）

| 位置 | 语义 | 并行下的问题 |
|---|---|---|
| `internal/download/progressline.go:18-21` | `progressLine{enabled,lastLen}`：**每条进度行私有** | 「上一帧多长」是私有的，A 行比 B 行短时 B 的补空格逻辑按自己的长度算 → 残影 |
| `progressline.go:28-40` | `draw`：`\r` + 覆盖 + `SetProgressLineActive(true)` | 只认「当前行」；N 个实例都以为自己独占这一行 |
| `progressline.go:43-53` | `clear`：`\r` + 空格擦除 + `SetProgressLineActive(false)` | 收尾**无条件**擦当前行——擦掉的可能是另一个任务的进度 |
| `internal/download/progress.go:139`、`downloader.go:1013` | 每次下载 `newProgressLine()`（单线程/多线程聚合各一条） | N 个下载 = N 条实例 |
| `internal/download/pacer.go:13-21,119-173` | 每下载一个重绘循环，上限 ≈60fps + 125ms 心跳 | 终端写入量与 `ConsoleLock` 竞争 ×N（10 任务 ≈ 600 次锁内 stdout 写/秒） |
| `internal/util/logger.go:108` | `ConsoleLock` 串行化控制台写入 | **只保证单次写入不撕裂**，不分配行、不认识「多行块」 |
| `logger.go:142-146` | `progressLineActive` 单个 `atomic.Bool`：这一行归进度条 | 全局只表达「有一行被占用」；两个下载都占用时无法区分，先收尾的那个会把标志清掉 |
| `logger.go:148-160` | `consoleWrite`：`if progressLineActive.Swap(false) { 补一个 \n }` | 假定**最多一行**被占用：N 行被占用时只补 1 个换行 → 日志盖在其余进度行上 |
| `internal/cli/interrupt.go:80-82` | Ctrl+C 时清进度行标志再打印 | 同一单行假设；并行时只清得掉「最后那个」的观感 |
| `internal/download/downloader.go:1052-1058` | `isTerminalOut` 看 `os.Stdout`（进程级） | 是进程级判断，不可能「按任务」区分是否画条 |
| `internal/download/progress.go:313-348` | 宽度预算按**整行**宽度算（`TerminalWidth`） | 没有「N 行块」的高度概念；A 需要重新定义高度与宽度 |

### 1.3 其它跨运行共享状态

| 位置 | 现状 | 并行下要做什么 |
|---|---|---|
| `internal/workflow/workflow.go:2041-2073` `pathLocks` | 进程级、按产物路径互斥，注释里已写明「两个并发任务（serve 任务或批量命令）会同时通过『已存在』检查」 | **已就绪**，无需改 |
| `workflow.go:344-345` `productLock` | 每页一把页级锁 | 已就绪 |
| `workflow.go:1591` `os.Chdir(full)` | **进程级**工作目录 | 必须在**任何下载开始前**完成（或在下载阶段禁改），否则并行任务互相改 cwd，相对路径产物会落错目录 |
| `workflow.go:1872,1877,1892,1899` `muxer.FFMPEG/MP4BOX` | 全局句柄赋值 | 探测/赋值前移到运行开始，或加锁；否则 A 任务混流时可能读到 B 任务写了一半的值 |
| `workflow.go:2104-2114` `stdinReader` + `Fscanf` | 进程级共享 stdin 接缝（自 2.15.1 起为 `atomic.Pointer[stdinSource]`，见台账 §4.67 的竞争修复；位置以代码为准），`-i` 提示直接读它 | 并行 + `-i` = 多个任务抢同一 stdin（提示互相交错、输入被别的任务吃掉）→ **显式互斥**（并行时 `-i` 报错或强制串行） |
| `internal/download/progressjson.go:16-19` `progressJSONEnabled` | 进程级开关（`--progress-json`） | 语义上可以共享（整个进程一个设置），但 §3.3 要补帧的任务标识 |
| `internal/cli/root.go:781-841` 批量循环 | `batchBytes` 是 `atomic.Int64` ✓；`firstErr` 是**裸变量**（`root.go:806-808`） | 并行目标下 `firstErr` 是数据竞争（`go test -race` 会报），要加锁或改成 channel |
| `upsertPending/removePending`（`root.go:812-816`） | 每次目标结束写未完成任务文件 | 并行写要串行化（或在汇总处统一落盘），避免丢更新 |

---

## 2. 评估项①：终端进度条渲染层的竞态现状

### 2.1 证据链（单行假设）

1. 每次下载创建一条进度行：`internal/download/progress.go:139`（单线程路径）、
   `internal/download/downloader.go:1013`（多线程聚合路径）。
2. 一条进度行只会「回到行首重绘」：`progressline.go:36` `fmt.Print("\\r" + text)`；
   擦除也是「当前行」：`progressline.go:49`。
   代码里**没有任何**「上移 N 行 / 分配某一行」的机制（全仓无 `\\x1b[` 上移序列）。
3. 并发保护只有一把互斥锁与一个全局布尔：`util.ConsoleLock`（`logger.go:108`）+
   `progressLineActive`（`logger.go:143`），语义是「**这一行**归进度条」（注释原文），
   而不是「这 N 行归进度条区」。
4. 日志侧的配套假定：`consoleWrite` 只在「进度行还没收尾」时补一个换行
   （`logger.go:156`），即假定屏幕上至多有一行进度。

### 2.2 失效模式（按严重度排序）

| # | 现象 | 证据 | 严重度 |
|---|---|---|---|
| F1 | N 条进度互相覆盖，屏幕上只剩「最后写入者」的帧，且短帧不擦净长帧的尾巴 | `progressline.go:32-40`（`lastLen` 私有）+ `progress.go:139` | 高（进度完全不可读） |
| F2 | 任务结束的擦行把另一个任务的进度擦成空白；该任务的后续日志紧接着落在同一行 | `progressline.go:43-53` + `pacer.go:140-142`（done→clear） | 高 |
| F3 | 日志与进度错位：只补 1 个换行，其余进度行被日志覆盖；`progressLineActive` 被先收尾者清成 false 后，后续日志**不再换行**，直接叠在进度条上 | `logger.go:148-160` | 高（观感最差，且日志会串行） |
| F4 | 终端写入与锁竞争 ×N：每下载 60fps（`pacer.go:17`）全都要拿 `ConsoleLock` 写 stdout | `pacer.go:13-21`、`logger.go:152-154` | 中（吞吐与体验） |
| F5 | 宽度/高度都按「一行」定义：帧宽预算按整行宽度算，没有块高度概念 | `progress.go:313-348` | 中（A 方案要重做） |
| F6 | 平台差异：`\r` 覆盖在 Windows conhost / dumb 终端 / 日志重定向下的表现不一致；A 需要 `ESC[<n>A` 上移，属**新增**平台风险面 | `progressline.go:36`（只用 `\r`）；`internal/util/terminal.go:79` `TerminalWidth` 已有非 TTY 回落 | 中 |

### 2.3 为什么 CI 看不到

终端渲染只在 `isTerminalOut()` 为真时发生（`downloader.go:1052-1058`），而测试/CI 的 stdout 是管道：
既有用例只能钉**纯函数帧**（`renderProgressFrame`/`renderProgressFrameAt`）与**时序**（`renderProgressBar` 接缝、
`pacer_test.go` 的注入时钟）。也就是说：**并行渲染的正确性没有可机械验证的观测面**，
这正是选 B（不新增渲染几何）而不是 A 的核心理由之一。

---

## 3. 评估项②：SSE/serve 并发与新并行下载的交互

### 3.1 链路（一次字节进度的事件流）

```
下载协程（progressReader.renderLoop / renderAggregateProgress 的 render 闭包）
   └─ pr.notifyObserver(...)                      progress.go:156-157、downloader.go:1041-1042
        └─ ctx 里的观察者（每任务一份）            server/progress.go:116-120 taskDownloadContext
             └─ APIServer.publishDownloadProgress  server/progress.go:138-160
                  ├─ task.mu 下记任务级字节映射    progress.go:143-144
                  ├─ 按任务节流（默认 100ms）      progress.go:53-55,145-149
                  └─ hub.publish（非阻塞、队列满丢帧） events.go:135-156
                       └─ 每客户端 64 深队列 → SSE 处理器 → 浏览器  events.go:41-43
```

### 3.2 已经就绪的部分（**不需要改**，逐条给证据）

1. **节流按任务**：`lastProgressPublish` 是 `DownloadTask` 的字段，在 `task.mu` 下读写
   （`server/progress.go:143-149`）→ N 个任务互不影响，天然并行安全。
2. **发布不阻塞下载**：`hub.publish` 在锁内做非阻塞发送，队列满的客户端丢帧（`events.go:150-155`）；
   观察者回调被要求「必须快速返回」（`progress.go:124-127`）。
3. **闸门与会话隔离**：执行槽 `semaphore`（`server.go:271`）、待办上限 `acceptLimiter=9×N`（`server.go:272`）、
   `/get-tasks` 查询上限 32（`server.go:623-636`）；每个任务自己的 `ctx/cancelFn`（`server.go:1060-1067`）。
4. **产物互斥**：`pathLocks`（`workflow.go:2041-2073`）保证两个任务不会同时写同一个产物。
5. **任务级字节口径**（`TotalDownloadedBytes`）按文件身份累计、不回跳（`server/progress.go:11-37`）。

### 3.3 需要处理的三件事

| # | 问题 | 证据 | 建议 |
|---|---|---|---|
| S1 | `--progress-json` 的帧**没有任务/分P标识**：并行后 stdout/stderr 上是多路交错的事件流，消费方无法归属 | `progressEvent{percent,downloaded,total,speed,state}`（`progressjson.go:25-32`） | 增补**可选**字段（如 `"page":3`、`"file":"..."`），保持 F5 契约向后兼容（旧消费方忽略新字段）；终端降级(B)下 `--progress-json` 成为唯一机器面，这个标识是必须的 |
| S2 | 回放历史 `eventHistory=64` 与队列 `eventClientBuffer=64` 会被「进度帧」灌满：晚连/重连的客户端可能**看不到 task_start/task_done 等生命周期事件**（并行时进度帧数量 ×N） | `events.go:40-46,104-120,146-149` | 历史缓冲区只留「非进度帧」，或按类型分环形缓冲；进度帧仍实时广播 |
| S3 | Web UI 只渲染「当前任务」一条进度 | `server/progress.go:132` 注释指向 `webui/index.html` 的 `renderCurrent` | 并行落地时 UI 改为「任务列表 + 各自百分比」；本期不改 UI，但要登记为已知限制 |

---

## 4. 评估项③：方案定案

### 4.1 方案 A —— 多行进度条（渲染器支持 N 个槽位，每任务一行）

要做的：① 进程级「进度区」渲染器（槽位注册/注销，按任务 ID 稳定排序）；
② 每次重绘整块：上移 N 行重画（`ESC[<n>A`）而不是 `\r` 覆盖；③ 区域高度 = min(槽位数, 终端高-1)，
超出时合并/截断；④ 日志必须打印在区域**下方**并重画区域（现有 `consoleWrite` 的「补一个换行」模型要重写）；
⑤ 单行宽度预算改成「块宽度预算」；⑥ 非 TTY/dumb 终端/重定向的降级；⑦ 与 `--progress-json` 互斥规则；
⑧ golden 帧用例（字节级）与平台差异用例。

成本：**4~6 人日**（核心 2~3 人日 + 用例/降级/平台 2~3 人日）。
风险：**最高**——失败模式是终端被写乱（残留、滚动条污染、日志错位），且 §2.3 说明 CI 无法真机观测，
回归只能靠字节级 golden 帧，维护成本长期存在。价值：N 个并行任务同时看到 N 条实时条，观感最好。

### 4.2 方案 B —— 并行时终端降级为边界日志（推荐）

要做的：① 新增一个**进程级**渲染模式开关（如 `download.SetTerminalProgress(bool)`，或复用
「并行度>1」的判定）；关闭时走**已有的非终端路径**——`renderLoop` 在 `!isTerminal` 时本就只跑观察者回调
（`progress.go:131-138`），多线程侧 `renderAggregateProgress` 在 `!line.enabled && observer==nil` 时直接返回
（`downloader.go:1014-1017`）；② 编排层在**边界**打日志：`[3/8] P3 开始下载…` / `…完成 12.4s 6.2 MB/s`
（现有 `开始下载P3视频...` / `下载P3完毕` 已在，只需补「第几个/共几个」与耗时）；
③ `--progress-json` 与 SSE 完全不变（细节在这里）；④ 并发=1 时保持今天的单行进度条（零回归）。

成本：**约 1 人日**（开关 + 边界日志 + 用例），加上 §6 表 2 的并行前置项才是总成本。
风险：**低**——失败模式是「并行模式下没有实时条」，回退 = 关掉开关（一行）。

### 4.3 推荐与对比

| 维度 | A 多行进度条 | B 边界日志降级（推荐） |
|---|---|---|
| 复杂度 | 高（槽位/块重绘/日志下移/高度截断/平台降级） | 低（复用既有非终端路径 + 边界日志） |
| 用户价值 | N 条实时条（好看，但并行时人也读不过来） | 「谁在跑、谁完了、多快」清晰可读；细节在 `--progress-json`/SSE/web UI |
| 回退风险 | 高（终端写乱，CI 观测不到；§2.3） | 低（一个开关；无新增 ANSI 面） |
| 现在的问题 | 需要它才能修 serve 单行互踩 | **同样修掉** serve 单行互踩（并行时不再画条） |
| 机器面影响 | 与 `--progress-json` 并存需新规则 | 不动（细节全留给机器面） |
| 工作量 | +4~6 人日 | +1 人日 |

**推荐 B**，并把 A 记为「后续 UX 升级」：等并行下载真正成为常用路径、且用户明确要「多条实时条」时再评估。
判断依据的权重顺序是：**先让并行可用且不破坏观感 → 再把观感做好**（ROADMAP 的「小步、有证据」节奏）。

### 4.4 分档规则（B 的落地形态）

| 并发度 | 终端行为 |
|---|---|
| 1（默认 / `--concurrency 1`） | 与今天完全一致：单行实时进度条（含分P游标、侧车行） |
| >1 | 不画进度条；每个「文件/分P」边界一行（`[i/N] P3/8 开始…` / `…完成 12.4s 6.2 MB/s`）；收尾一条汇总行（含并发数与整体吞吐） |
| 任意 + `--progress-json` | 终端不画条（今天的行为），逐行 JSON 输出（§3.3 S1 补任务标识） |
| 任意 + serve | 终端按上面的并发度规则；SSE/任务 API 不变 |

---

## 5. 评估项④：游标 / 侧车行 / 汇总行在并行下的语义调整清单

| 对象 | 今天（串行） | 并行下（B 路线） | 位置 |
|---|---|---|---|
| 分P游标 `ProgressCursor`（`P3/8`） | `DownloadConfig.ProgressCursor` 每页设置，进度行行首显示 | 并发=1 不变；并发>1 时游标移到**边界日志**前缀（`[i/N] P3/8 …`）；`--progress-json` 帧里带 `page` 字段（S1） | `internal/download/downloader.go:44-46`、`internal/workflow/infolines.go`（`progressCursorFor`）、`internal/workflow/workflow.go`（每页赋值） |
| 侧车状态行 `侧车: 字幕 ✔ · 弹幕 ✔ · 封面 ✔` | 每页媒体下载前打一行（`printSidecarLine`） | 并发>1 时该行加任务前缀（`[i/N] 侧车: …`），保证多任务日志可归属；不启用项仍不出现；整行不打的条件不变 | `internal/workflow/infolines.go`（`enabledSidecars/renderSidecar…`）、`internal/download/sidecar.go` |
| 收尾汇总行 `下载完成：成功 N 个，失败 M 个，耗时 X · 平均 Y MB/s` | 每批一条；耗时=墙钟，平均=落盘字节/墙钟 | 语义要**说清**并补充：`· 并发 K`；耗时在并行下是墙钟（用户会读成加速比），平均是**整体吞吐**（并行下更有意义）；`firstErr` 竞态与 pending 写要串行化（§1.3） | `internal/cli/root.go:736-746,781-841`；用例 `internal/cli/downloadsummary_test.go` |
| 「开始/完毕」日志 | `开始下载P3视频...` / `下载P3完毕` | 补 `[i/N]` 前缀与耗时，成为并行下的主要观感面 | `internal/workflow/workflow.go`（各阶段日志） |
| `-i` 交互 | 提示 + `stdinReader` 读序号 | 并发>1 时**拒绝或强制串行**（多任务抢 stdin 必然错乱） | `workflow.go:2104-2114`、`infolines.go`（`chooseTierInteractive`） |

---

## 6. 实现点清单（后续实现任务可直接引用）

### 表 1：方案 B 的最小实现（必须）

| # | 改动点 | file:line | 改法 | 用例 | 工作量 |
|---|---|---|---|---|---|
| B1 | 终端渲染模式开关 | `internal/download/progressline.go:23-25`、`progress.go:131-138`、`downloader.go:1013-1017` | 新增进程级开关（`SetTerminalProgress(bool)`，默认 true）并让 `newProgressLine`/`renderAggregateProgress` 尊重它；关时不画条、只跑观察者循环 | `internal/download` 新增：开关关闭时「零终端写入 + 观察者仍收到首帧/末帧」（用 `isTerminalOut` 注入 + 捕获 stdout 断言为空） | 0.5d |
| B2 | 编排层边界日志 | `internal/workflow/workflow.go`（`下载P%d完毕` 一带）、`internal/cli/root.go:781-841` | `[i/N]` 前缀 + 每文件耗时/速率（复用 `formatSpeed`） | `internal/workflow` / `internal/cli` 用例：逐字断言前缀与耗时字段 | 0.5d |
| B3 | 并发开关与调度 | `internal/cli/urlsfile.go:62-78`（`runTargets`）、`internal/cli/root.go:777-841` | `--concurrency K`（CLI 批量/分P级）；`K==1` 走今天的顺序路径；`K>1` 时把 B1 的开关置为关 | 新增：K=3 时峰值并发计数=3（参照 `internal/server/concurrency_test.go:17-60` 的计数器判据，不用墙钟） | 1.0d |
| B4 | 汇总/pending 并发安全 | `root.go:806-808`（`firstErr`）、`root.go:812-816`（pending 写） | `firstErr` 加锁或用 channel 汇总；pending 写进临界区 | `go test -race` 跑新并发用例；pending 文件内容断言 | 0.5d |
| B5 | **serve 启动接线**（兑现「B 顺带修 serve 互踩」） | `internal/server/server.go`（Run） | `maxConcurrent>1` → 启动时 `SetTerminalProgress(false)`（终端降级为边界日志，SSE 不受影响；`==1` 保持今天的单行进度条） | serve 用例：并发>1 启动后终端进度被禁用 + SSE 事件照发 | 0.25d |

### 表 2：并行前置项（不做会出真 bug）

| # | 改动点 | file:line | 改法 | 用例 | 工作量 |
|---|---|---|---|---|---|
| P1 | `os.Chdir` 进程级 | `internal/workflow/workflow.go:1591` | 下载阶段开始前完成；或并行模式下禁止改 cwd（显式报错） | 用例：K=2 且两个目标相对路径不同产物目录，断言产物落在各自目录 | 0.25d |
| P2 | `muxer` 全局句柄 | `workflow.go:1872-1899` | 二进制探测/赋值前移到运行开始（单线程段），并发段只读 | 用例：并发下混流仍用同一路径（假可执行文件 dump argv） | 0.25d |
| P3 | `-i` 与并发互斥 | `workflow.go:2104-2114`（stdin 接缝，现 `atomic.Pointer` 形态）、`internal/workflow/infolines.go` | `K>1 && -i` → 启动即报错（或在调度层强制 K=1）并打印理由 | 用例：逐字断言错误文案与「没有进入下载」 | 0.25d |
| P4 | `--progress-json` 任务标识 | `internal/download/progressjson.go:25-32` | 增补可选字段（`page`/`file`），保持旧字段不变 | `internal/download/progress_json_test.go` 增补：新字段出现且旧断言不改 | 0.5d |
| P5 | 回放历史只留非进度帧 | `internal/server/events.go:146-149` | 历史缓冲跳过 `task_progress`（实时广播不变） | `internal/server` 用例：并行灌进度帧后新客户端仍能收到 `task_start` | 0.5d |

### 表 3：可延后（本期不做，登记为已知限制）

| # | 项 | 说明 |
|---|---|---|
| C1 | 方案 A 多行进度条 | 见 §4.1；等并行成为常用路径后再评估 |
| C2 | Web UI 任务列表并行渲染 | `server/progress.go:132` 指向的 `renderCurrent` 单当前任务；并行时 UI 只显示一个 |
| C3 | aria2c 路径的并行进度 | aria2c 是黑盒（台账 §4.57），并行下仍只有边界信号 |

**合计**：B 路线 B1~B5 + P1~P5 ≈ **4.5 人日**（含用例），量化测量另计 0.5 人日（见 §7.3）。

---

## 7. 验收、回归网与量化

### 7.1 实现任务必须产出的用例（新增）

1. `internal/download`：**渲染模式开关**——关时「stdout 零写入」但观察者收到首帧与末帧（`progress.go:131-138` 的既有分支）；
   开时行为与今天逐字一致（既有帧用例不改）。
2. `internal/cli`：**并发闸门**——K=3 / 6 个目标，峰值并发=3（计数器判据，参照 `concurrency_test.go`）；
   `K=1` 时输出与今天逐字一致（顺序、进度条路径）。
3. `internal/cli`：**汇总行**——并行批次的「耗时=墙钟、平均=总字节/墙钟、`并发 K` 字段」逐字断言；
   `firstErr` 与 pending 文件在 `-race` 下无告警。
4. `internal/workflow`：**产物落点**——K=2 + 相对路径（P1）、`-i` 与并发互斥文案（P3）。
5. `internal/download/progress_json_test.go`：**新字段**（P4）不影响既有断言。
6. `internal/server`：**回放不被进度帧挤掉**（P5）。

### 7.2 变异验证（撤掉实现必须变红）

- 撤掉 B1 的开关（并行时仍画条）→ 「关时 stdout 零写入」红；
- 撤掉 B3 的调度（退回顺序）→ 峰值并发断言红（只有 1）；
- 撤掉 P4 的字段 → 新字段断言红；
- 撤掉 `pathLocks`（`workflow.go:2053`）→ 同产物并发用例红（这条今天的用例可能已覆盖，需确认）。

### 7.3 量化（AGENTS 证据门槛：优化要给改前/改后数字）

必须给出**同一台机器、同一批任务**的墙钟与吞吐对比（至少三组）：

| 场景 | 改前（串行） | 改后（K=3） | 说明 |
|---|---|---|---|
| 6 个中等目标（各 1 个分P） | 墙钟 T1 / 平均 X1 MB/s | 墙钟 T2 / 平均 X2 MB/s | 收益主要来自多文件的握手与等待重叠 |
| 1 个 8 分P稿件 | 同上 | 同上 | 分片并行已由 `--multi-thread` 覆盖时的边际收益要单独说明 |
| 高负载本机（负载≈核数） | 观察是否反而变慢 | 同上 | 明确「并行不是越多越好」的默认值依据 |

没有数字不算优化：结论与默认 `--concurrency` 取值都要由这三组数字支撑。

### 7.4 记录要求（本仓规矩）

- 并行下载是本仓**自研**能力（上游只有单文件多线程）→ 实现落地时在 `docs/UPSTREAM_ALIGNMENT.md` §4.x
  登记「有意偏离/自研」；`CHANGELOG.md` 写清「差异点 + 用户价值」；
- `docs/ROADMAP.md:140` 的条目在实现落地后移到「已完成」并附量化数字；
- 终端降级是**观感**变更 → CHANGELOG 需要一句「并行时终端不再画进度条，细节见 `--progress-json`/SSE」。

---

## 8. 边界与未做的事

- 本文件**不改任何产品代码**；所有 file:line 是评估时的实际位置（改动前请以代码为准）。
- 未实测真机终端行为（§2.3：CI 无法观测 TTY），F1~F3 的现象由代码路径推导，**实测步骤**留给实现任务的第一步（先跑
  `serve` 两个任务 + 抓终端字节流，作为「改前」证据）。
- 未评估「并行度对风控（412）的影响」：并行会增加同一出口的并发请求数，实现时应把
  `--concurrency` 与既有 UA 轮换/退避（`internal/download/retry_policy.go`）一起回归，并在 CHANGELOG 提示默认值保守。
