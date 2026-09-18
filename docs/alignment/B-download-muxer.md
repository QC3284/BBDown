# 片 B：下载 / 混流 / 外部程序 / 进度与归档

> 审计基线：上游 C# `v1.6.11` → `v1.6.19`（本地 git 对象库，未联网）。
> 条目来源：`git show v1.6.19:CHANGELOG.md`（1.6.12~1.6.19）+ `git show v1.6.19:docs/REVIEW_FINDINGS.md`，并用 `git log --contains` 把代码改动归属到具体 release。
> Go 侧只读，未修改任何源码。所有判定均附 `internal/...:行号` 证据；无法取证的一律 `❓`。

## 结论摘要

- 审核条目数：**39**（✅ 10 / ⚠️ 10 / ❌ 15 / ➖ 3 / ❓ 1）
- 最高风险 3 条：
  1. **#15（P0）ffmpeg 输出选项位置错误（v1.6.16 已修）**：`-metadata:s:s:N` / `-metadata:s:a:N` / `-disposition` 紧跟各自 `-i` 之后，被 ffmpeg 当输入选项——本机真实 ffmpeg（n9.0.1）复现：`-i cover.jpg -disposition:v:1 attached_pic -i chapters` 退出码 234（`cannot be applied to input url chapters`）。Go 侧只要"封面 + 章节"（B 站视频极常见）或"≥2 条字幕"或"背景音轨"就混流必败。
  2. **#22（P0）mp4box 分支丢弃配音/背景音轨**：`muxByMp4box` 根本没有 `audioMaterial` 形参，杜比视界自动切 mp4box 时已下载的配音轨被静默丢弃，且随后被清理逻辑删除（数据永久丢失，无任何告警）。
  3. **#30 / #31（P0）服务器可控路径段与占位符未净化**：`<aid>/<cid>/<epid>`、`<dfn>/<res>/<fps>/<videoCodecs>/<audioCodecs>` 全部裸替换（RF-58/63/73 在 C# 已收口，Go 全未覆盖），镜像站 / `--insecure` 中间人可让产物写出 `--work-dir` 之外，且 `aid` 还直接作为目录名参与下载与删除。
- 本片整体判断：
  - **错误隔离/取消语义这一类条目 Go 反而更好**：Go 用返回值 + `bool` 页面结果而非异常过滤器，"单 P 失败不拖垮整批"由结构保证（工作流 282-291），因此 RF-14/43/44/72、RF-31 等一批"异常类型逃逸两级过滤器"条目在 Go 侧天然等价（✅）。
  - **真正的系统性缺口在两处**：① 外部程序参数构造（ffmpeg 选项位置、mp4box 转义/音轨、探针超时、临时产物事务化）；② 路径与占位符净化族（RF-58/63/73）与文件名/扩展名决策（SubOnly `.srt`）。这两族共 9 条 ❌，其中 4 条 P0。
  - **清理一致性与可测试性偏弱**：混流失败路径不清理、字幕从不清理、`.ts` 中间产物无 finally、进度/入档无生产级可测单元。

## 条目明细

| # | 来源 | 上游变更 | Go 现状 | 判定 | 证据（Go 侧 file:line） | 建议动作 | 优先级 |
|---|---|---|---|---|---|---|---|
| 1 | v1.6.12 | 下载路径独占锁命中跳过时，清理败者任务已下载的临时音视频/字幕文件 | Go 完全没有 `RunWithPathLock` 一类的目标路径排他锁：同一 savePath 的并发任务各自下载、各自混流覆盖 | ❌ | `internal/workflow/workflow.go:492-495`（仅"存在即跳过"，无锁无败者清理）；全库无路径锁（grep `Semaphore`/`fileLock` 仅命中 `internal/download/downloader.go:410` 的 errMu、`internal/substore/substore.go:32`） | 引入 savePath 级互斥锁，锁内做权威存在性判定 + 败者临时文件清理 | P2 |
| 2 | v1.6.12 | 为 FLV 分支补齐弹幕下载、`--danmaku-only`、`--cover-only` | 弹幕 / 仅弹幕 / 仅封面三块都在 FLV 分支之前（结构上已覆盖 FLV），但 `--cover-only` **没有提前 return**，会继续走 FLV/DASH 全量下载与混流；无封面资源时静默当成功 | ⚠️ | `internal/workflow/workflow.go:532-557`（弹幕+仅弹幕，早于 627 的 FLV 分支）、`513-529`（cover-only 无 return）；对照 C# `Download.cs:689-706`（"封面保存成功后必须立即 return" + 无封面 return false） | cover-only 下载成功后立即 `return true`，无封面资源返回 false | P1 |
| 3 | v1.6.12 | 多线程分片遇不支持 Range 时立即抛错，不做无意义退避重试 | 分片请求收到 200（非 206）即返回 `ErrRangeNotSupported`，**不进入退避**，并整体降级为单线程下载 | ✅ | `internal/download/downloader.go:555`（`if err == ErrRangeNotSupported \|\| ctx.Err() != nil { return 0, err }`）、`437-441`、`118-126`（降级 singleDownload） | 保持；建议补一条"200 响应 → 单线程回退"的单测 | P2 |
| 4 | v1.6.12 / RF-14 | `NotSupportedException` 规范化为 `InvalidOperationException`，不穿透页面级/批级过滤器 | Go 无异常过滤器：`downloadRange` 返回 error → `multiThreadDownload` 收敛 → `DownloadFile` 返回 → 页面重试后记失败，批级继续 | ✅ | `internal/download/downloader.go:74`、`452-459`；`internal/workflow/workflow.go:282-291`（页面结果 bool，失败仅 append failedPages） | 无需改动 | — |
| 5 | v1.6.12 | `MergeFLV` 单分片路径免除 ffmpeg 依赖，直接移动 | 单分片直接 `os.Rename`，不启动 ffmpeg | ✅ | `internal/muxer/muxer.go:339-341` | 无需改动 | — |
| 6 | v1.6.12 | 多段 FLV 合并中间 `.ts` 用 try-finally 保证异常/取消时也清理 | 清理只在**全部成功之后**执行；转换失败（`return` at 349-351）或合并失败（`return` at 358-360）都会把已生成的 `.ts` 留在 aid 目录 | ❌ | `internal/muxer/muxer.go:343-367`（无 `defer`；350、358-360 提前 return 跳过 362-367 的清理） | 用 `defer` 清理 `tsFiles`（与 C# `finally` 同构），源分段仍只在成功后删 | P1 |
| 7 | v1.6.12 / RF-22 | `CheckFFmpegDOVI` 增加 5 秒异步超时并观察管道任务 | Go 用 `exec.Command(...).CombinedOutput()` **无任何超时**，ffmpeg 卡死则整个下载挂起；另 RF-22 后半（成功路径 5s 管道兜底）在 Go 无对应物（直接 `cmd.Stdout = os.Stdout`，无管道任务） | ❌ | `internal/muxer/muxer.go:192-206`（无 `context`/`timeout`） | 改 `exec.CommandContext` + 5s `context.WithTimeout` | P1 |
| 8 | v1.6.12 | 进度条刷新纳入 `Logger.ConsoleLock`，避免多任务并发字符交织 | 进度条直接 `fmt.Fprintf(os.Stdout, ...)`，与 logger 的互斥量无关；且 `Logger.Log` 自己也不加锁 | ⚠️ | `internal/download/progress.go:95`、`internal/download/downloader.go:593`；`internal/util/logger.go:53-58`（`Log` 无锁，仅 LogError/LogWarn/LogColor 用 `l.mu`） | 抽出统一控制台输出锁，进度条与日志共用 | P2 |
| 9 | v1.6.14 | 轨道清理并入 `finally`（`CleanupDownloadedTracks`）：成功/失败/异常都不残留 GB 级临时文件 | 清理仅在成功路径末尾执行（789-816），且**不清理已下载字幕**；混流失败（`return false` at 783）直接返回，已下载音视频仍由 789 之后的代码……实测不会执行（在 `return` 之后） | ⚠️ | `internal/workflow/workflow.go:778-787`（MuxAV 失败即 `return false`，跳过 789-816 的全部清理）、`789-816`（不删 `downloadedSubs`，582 行收集）；对照 C# `Download.cs:286`（finally 调 CleanupDownloadedTracks） | 把 789-816 收敛成 `defer` 式清理函数，并补字幕/章节清理 | P1 |
| 10 | v1.6.14 | 无主音轨时素材元数据下标起点修复（标题不再错位） | Go 恒发 `-metadata:s:a:0 title=原音频`，且素材下标从 1 起，未按"主音轨是否存在"分流 | ⚠️ | `internal/muxer/muxer.go:68-81`（69 行无条件标 s:a:0；73 `audioCount++` 后即用作下标） | 有主音轨时才标 s:a:0；`audioCount` 起点按 `audioPath == ""` 取 -1/0 | P2 |
| 11 | v1.6.14 | aria2c 进程级 6 小时兜底超时（防僵死永久占并发槽） | `exec.CommandContext(ctx, ...)` 直接继承下载 ctx，无独立 deadline | ❌ | `internal/download/downloader.go:648` | 加 `context.WithTimeout(..., 6h)` 包裹 aria2c 进程 | P2 |
| 12 | v1.6.14 | 未闭合引号不再吞掉整段 `--aria2c-args` 配置 | Go 完全不做引号解析，`strings.Fields` 按空白切分（带引号的含空格参数会被拆碎，含空格的值也无法保持单 token） | ⚠️ | `internal/download/downloader.go:643-645`（`strings.Fields(cfg.Aria2cArgs)`） | 实现引号感知拆分（未闭合引号尽力恢复语义） | P2 |
| 13 | v1.6.15（提交 df4eba9，CHANGELOG 未单列） | VOD 媒体流 60s 读停滞看门狗：黑洞/半死 TCP 下 `ReadAsync` 不再永久挂起 | 单线程路径直接 `io.Copy(out, resp.Body)`，且 `DownloadClient()` 返回的 `http.Client` **无 Timeout** → 停发数据即永久挂起（serve 下钉死并发槽） | ❌ | `internal/download/downloader.go:329`；`internal/util/http.go:234-237`（`DownloadClient` 仅 Clone transport，未设 Timeout） | 增加"每收到数据重置"的停滞计时（或给下载客户端加分段超时） | P1 |
| 14 | v1.6.15 | 分片扩展名判定统一走 `IsVideoClipPath`（RF-1：大小写敏感漏网） | Go 分片名恒为常量后缀 `.vclip`，不存在"按扩展名判定轨道类型"的判定点，该 C# 缺陷类无对应物 | ➖ | `internal/download/downloader.go:494-496`（`clipPath` 固定 `.vclip`） | 无需改动 | — |
| 15 | v1.6.16 | ffmpeg 输出选项（`-metadata:s:s:N`/`-metadata:s:a:N`/`-disposition`）统一收集到 `outputArgs`，在全部 `-i`/`-map` 之后追加 | Go 仍把三类输出选项紧跟各自 `-i` 追加 → 被 ffmpeg 当作输入选项。**已用真实 ffmpeg n9.0.1 复现**：`-i in.mp4 -i cover.jpg -disposition:v:1 attached_pic -i chapters ...` → exit 234，`Option disposition:v:1 cannot be applied to input url chapters`；两条字幕、背景音轨 metadata 同样报错 | ❌ | `internal/muxer/muxer.go:68-81`（音频素材 metadata）、`89-102`（字幕 metadata，紧跟 `-i s.Path`）、`104-110`（disposition，紧跟 `-i pic`）、`126-128`（其后才是 chapters `-i`）、`130-132`（`-map` 更晚） | 引入 `outputArgs` 收集，全部 `-map` 之后统一追加（与 C# v1.6.16 同构），并加"输出选项必须在最后一个 `-i` 之后"的单测 | **P0** |
| 16 | v1.6.16 | `--comments` 评论保存前自动创建父目录（此前父目录不存在即永久保存失败） | `SaveCommentsJSON` 直接 `os.WriteFile`，不建目录；评论保存点在（多 P 模板的）输出目录创建之前 → 父目录不存在 → 失败被降级为警告，评论静默丢失 | ❌ | `internal/workflow/workflow.go:611-614`（保存点，早于 693-694 的目录创建）、`internal/util/comment.go:75-81`（无 `MkdirAll`） | 保存函数内 `os.MkdirAll(filepath.Dir(path))`（自包含），与 C# 同构 | P1 |
| 17 | v1.6.16 | 保存路径模板按【实际下载分 P 数】决策；补零宽度仍用"全部分 P 总数" | Go 两处都用**筛选后**的 `len(pagesInfo)`：模板决策 ✅（与上游一致），但 `<pageNumberWithZero>` 的补零宽度也用了筛选值，与上游"用视频总 P 数"不同 | ⚠️ | `internal/workflow/workflow.go:215-227`（模板决策用筛选后数量）、`481` → `internal/download/downloader.go:844`（`digits(pagesCount)`） | 拆成两个计数：模板决策用选中数，补零宽度用 `len(vInfo.PagesInfo)` | P2 |
| 18 | v1.6.17 / RF-23 | 事务化混流临时产物 `.muxing-{guid}` 改为 `.muxing-{guid}.mp4`，不再依赖 GPAC 扩展名推断 | Go **没有事务化混流机制**：ffmpeg/mp4box 直接写最终 `savePath`。既无扩展名兼容问题，也无"半成品当成品"防护——混流中断留下的半截 mp4 会被 492 行的"存在且非空即跳过"永久当作已完成 | ❌ | `internal/workflow/workflow.go:778`（outPath 直接为 savePath）、`internal/muxer/muxer.go:177`（`-f mp4 -- outPath`）、`311`（mp4box `-new outPath`）；跳过判定 `internal/workflow/workflow.go:492-495` | 引入 `savePath + ".muxing-{guid}.mp4"` 临时产物，校验非空后原子替换；失败/取消清理 | P1 |
| 19 | v1.6.17 / RF-18 | SubOnly 模式 ASS 内容字幕产物扩展名按源内容决定（不再无条件 `.srt`） | Go 无条件 `+ "." + s.Lan + ".srt"`，ASS 内容字幕被改名为 `.srt`，播放器无法渲染 | ❌ | `internal/workflow/workflow.go:588-591`（`strings.TrimSuffix(outPath, ext) + "." + s.Lan + ".srt"`） | 按源文件扩展名决定目标扩展名（`.ass` → `.{lan}.ass`） | P1 |
| 20 | v1.6.17 / RF-5 | ffmpeg `creation_time` 固定 InvariantCulture | Go 用固定布局 `2006-01-02T15:04:05.000000Z` + UTC 格式化，语言环境无关（等价） | ✅ | `internal/muxer/muxer.go:161-163` | 无需改动 | — |
| 21 | v1.6.17 / RF-6 | mp4box `-itags` 的 `cover` 值走 `EscapeString`（Windows 路径 `\` 被当转义序列消费 → 封面静默丢失） | 同函数其它 itags 值都过了 `escapeString`，唯独 `pic` 裸拼 | ❌ | `internal/muxer/muxer.go:272-276`（`metaArg.WriteString(pic)` 未转义）；对照 279/281/289/291/293 的 `escapeString(...)` | `metaArg.WriteString(escapeString(pic))` | P1 |
| 22 | v1.6.17（提交 3e0e7fc） | mp4box 分支编入配音/背景音轨（否则杜比视界自动切 mp4box 时这些轨道被静默丢弃，且随后被清理删除） | `muxByMp4box` 形参里**完全没有 `audioMaterial`**，配音/背景轨在 mp4box 路径下既不进入 `-add` 链也不进 `-udta`，而调用方随后仍会删除这些已下载文件（789-816 之外的 807-809 行） | ❌ | `internal/muxer/muxer.go:24-38`（MuxAV → muxByMp4box 不传 audioMaterial）、`227`（签名无该参数）；`internal/workflow/workflow.go:778-781`（调用点）、`807-809`（删除 `backgroundMaterial`） | mp4box 分支补 `-add <path>:lang=und` + `-udta N:type=name:str="..."` | **P0** |
| 23 | v1.6.17 | `EscapeString` 补 CR/LF 折叠（换行会破坏 itags token 语法）；转义只在 mp4box 分支做（ffmpeg 走 argv 直传，提前转义会把字面 `\\`/`\"` 写进元数据） | Go 的 `escapeString` 只处理 `\` 与 `"`，不折叠 CR/LF；且 ffmpeg 分支也照用（双重转义） | ⚠️ | `internal/muxer/muxer.go:41-47`（无 CR/LF）；ffmpeg 分支使用点 `76-79`、`96-97`、`147-159` | 折叠 CR/LF 为空格；ffmpeg 分支传字面值，转义只在 mp4box 分支 | P2 |
| 24 | v1.6.17 / RF-8 + RF-20 | 跳过路径清理一致性：锁内 Skipped 补封面清理、dash 裸删统一包裹、`chapters*` 前缀兜底 | Go 的**唯一跳过判定**（492-495）早于所有装饰性下载（封面 498、弹幕 532、字幕 562），结构上不产生同类残留 ✅；但成功混流后从不删除已下载字幕，aid 目录因非空永不清理 ⚠️；章节文件由 muxer 自清（184-186、318-320）✅ | ⚠️ | `internal/workflow/workflow.go:492-495`（跳过早于装饰下载）、`789-816`（清理不含 `downloadedSubs`，收集于 582）；`internal/muxer/muxer.go:184-186`、`318-320` | 清理流程补字幕/章节清理（前缀匹配 `chapters*`） | P2 |
| 25 | v1.6.18 / RF-21 | aria2c `--input-file=-` 写入前剔除 URL 与 Cookie 中的 `\r`/`\n`（换行即指令行分隔符） | Cookie 已净化 ✅，URL **未净化** ❌ → 服务器的 CDN 地址含换行时仍可注入任意 aria2c 指令行 | ⚠️ | `internal/download/downloader.go:667`（cookie `strings.NewReplacer("\r","","\n","")`）、`659`（`sb.WriteString(url + "\n")` 未处理） | URL 写入前同样剔除 `\r`/`\n` | P1 |
| 26 | v1.6.18 / RF-31 | 轨道排序对服务器可控清晰度 id 用 TryParse 降级，两级过滤器补 Format/Overflow | `strconv.Atoi` 错误被显式丢弃（id 缺失/非数字 → 0，仅作并列 tie-break 降级） | ✅ | `internal/download/downloader.go:715-716` | 无需改动（可补一条畸形 id 单测） | — |
| 27 | v1.6.18 / RF-34 | `--skip-mux` 时跳过杜比视界探测（探测失败过滤器兜底为走 mp4box） | Go 探测调用**无 `!SkipMux` 门控**（每次 dfn=126 都会白起一次进程）；无异常逃逸面（exec 错误返回 false，不会中止整批） | ❌ | `internal/workflow/workflow.go:755`（条件缺 `!w.Cfg.SkipMux`）；对照 C# `Download.cs:755` | 条件补 `!w.Cfg.SkipMux` | P2 |
| 28 | v1.6.18 / RF-43 | 外部程序"已解析但不可启动"（Win32Exception）规范化为不穿透过滤器的类型 | Go 的 `exec.CommandContext(...).Run()` 把启动失败（`*exec.Error`/`*os.PathError`）作为 error 返回 → `MuxAV` 返回 → 页面 `return false`，批级继续 | ✅ | `internal/muxer/muxer.go:180`、`314`、`348`；`internal/workflow/workflow.go:782-783`、`282-291` | 无需改动 | — |
| 29 | v1.6.18 / RF-44 | `UnauthorizedAccessException` 补入两级过滤器；7 处仅捕 `IOException` 的清理子句对齐 | Go 清理路径基本忽略 `os.Remove`/`os.Rename` 错误，不会因本地权限错误把页面翻成失败 | ✅ | `internal/workflow/workflow.go:794-816`（删除返回值一律忽略）、`internal/download/downloader.go:258-259`、`454`、`467` | 无需改动 | — |
| 30 | v1.6.18 / RF-48 + RF-73 | 服务器可控 `aid/cid/epid` 经 `SanitizePathSegment` 单一收口（可穿越保存路径，且 aid 用于递归删除） | Go 的 `Page` 是纯字段结构无 setter 净化；`<aid>`/`<cid>` 裸替换；`page.Aid` 直接作为目录名参与创建/下载/删除 | ❌ | `internal/entity/entity.go:9-23`（无净化）；`internal/download/downloader.go:846-847`（`<aid>`/`<cid>` 裸替换）；`internal/workflow/workflow.go:504`、`664`、`693`（aid 直拼路径） | 在读取点对 aid/cid/epid 施加 `[0-9]+` 白名单或 `GetValidFileName(filterSlash)`，并在 `FormatSavePath` 兜底 | **P0** |
| 31 | v1.6.18 / RF-58 + RF-63 | `<dfn>/<res>/<fps>/<videoCodecs>/<audioCodecs>` 统一过 `GetValidFileName`（服务器透传值可含 `/`、`..`） | Go 这 5 个占位符仍是裸替换；title/pageTitle/ownerName 已净化（说明净化器存在但未覆盖） | ❌ | `internal/download/downloader.go:854-857`、`861`（裸替换）；对照 `835-840`（title 族过 `util.GetValidFileName`） | 该 5 项统一 `util.GetValidFileName(v, "_", true)` + 尾随点/空格裁剪 | **P0** |
| 32 | v1.6.18 / RF-60 | `Audio.shortCodecs` 的 `ToUpper()` 改 `ToUpperInvariant()`（tr-TR 下 `i`→`İ`） | Go 手写 ASCII 小写→大写映射，语言环境无关（等价，且原理上不可能触发 tr-TR 问题） | ✅ | `internal/entity/entity.go:129-140`；使用点 `internal/download/downloader.go:734`、`738` | 无需改动 | — |
| 33 | v1.6.18 / RF-45 | 免二压重发降级后不再丢失杜比/Hi-Res 音轨（列表重赋值只在"新文档接管"分支） | Go 侧**没有 pass1 免二压重发链路**（`ExtractTracks` 单次请求），该降级缺陷不可复现；但"重发取最高画质"能力本身在 Go 是否存在需与 E 片确认（互动式 FLV 选清晰度时会二次 `ExtractTracks`） | ❓ | `internal/parser/parser.go:59-78`（单次 `getPlayJSON` → `parseDomesticStreams`）；`internal/workflow/workflow.go:650-658`（唯一二次解析点，交互式 FLV） | 与 E 片对齐"免二压重发"是否有意省略；若省略，杜比轨仅在首次 dash 解析追加，需确认无覆盖 | P2 |
| 34 | v1.6.19 / RF-64 | 评论抓取 catch 白名单补齐（此前超时等逃逸后被页面级过滤器记为失败） | Go 评论块内任何 error 只 `LogWarn`，仅取消时返回失败（`ctx.Err()` 守卫）——比白名单更彻底 | ✅ | `internal/workflow/workflow.go:601-620`（尤其 605-609） | 无需改动 | — |
| 35 | v1.6.19 / RF-72 | `InvalidDataException`（64MB 响应体上限 / gRPC 帧校验）补入两级过滤器 | Go 侧解析错误经 `ExtractTracks` 返回 → 页面级重试 → 记失败，批级继续，结构上无逃逸面（注：Go 侧本身未实现响应体大小上限，属 D/E 片） | ✅ | `internal/workflow/workflow.go:352-367`（解析错误只影响本页）、`285-291` | 无需改动；响应体上限问题转 D 片 | — |
| 36 | v1.6.19 / RF-75（入档粒度） | 入档以 aid 为粒度：该 aid **全部分 P 处理完且无一失败**才写 `BBDown.archives` | Go 在**每个分 P 成功后**立即入档 → 多 P 稿件第 1 P 成功后入档，后续 P 失败被忽略；下次运行该 aid 全部分 P 被跳过（静默漏下） | ❌ | `internal/workflow/workflow.go:820`（`w.saveAidArchived(page.Aid)` 位于 `downloadOnePage` 成功返回前）、`277`（`checkAidArchived` 跳过）、`1474-1484`（实现） | 引入 aid 级 tracker（剩余计数 + 失败 aid 集合），全部分 P 处理完且无失败才写档 | P1 |
| 37 | v1.6.19 / RF-75（进度聚合 + 可测试性） | 抽生产类型 `ProgressAggregator`（跨分片累计、重试回退不越过 100%）并让测试直接驱动，消除假绿 | Go 无跨分片/跨文件进度聚合：多线程仅"分片成功后累加总量"，单线程每文件独立进度条；无生产级可测单元，测试目录也没有对应用例 | ⚠️ | `internal/download/downloader.go:403-405`（`totalBytes` 仅成功累加）、`565-609`（`renderAggregateProgress` 直接用 atomic 计数）；`internal/download/downloader_test.go`、`downloader_extra_test.go`（无进度/入档用例） | 抽出可注入的进度聚合 + 入档判定生产类型并补单测（与 RF-75 同构） | P2 |
| 38 | v1.6.14 / RF-27 | `FindBinaries` 写进程级静态工具路径（serve 并发理论面） | Go 同样是包级可变全局（`muxer.FFMPEG`/`MP4BOX` 由 `findBinaries` 赋值）；上游已判定"维持现状"（serve 下路径字段被清零、同值无冲突） | ➖ | `internal/muxer/muxer.go:20-21`；`internal/workflow/workflow.go:1359-1392` | 维持现状（如需彻底收口随 A 片 serve 配置项一起做） | — |
| 39 | RF-2 / v1.6.17 | CLI 命令层迁移 `AsyncCommand`，消除 `Task.Run + GetAwaiter().GetResult()` | C# 线程池/同步阻塞语义特有；Go 的命令入口本身就是同步阻塞调用，无 async-over-sync 死锁/饥饿面 | ➖ | — （Go 无对应机制） | 无需改动 | — |

## 归属其它范围的条目

- **RF-45（免二压重发降级丢杜比/Hi-Res 音轨）**：主代码面在 `BBDown.Core/Parser.cs` → **E（fetcher/parser）**。本片仅记录 Go 侧"无 pass1 重发链路"的观察（见 #33）。
- **RF-57（`ToolFinder` 在 CWD 搜 mp4decrypt/device.wvd）**：属 DRM 工具查找 → **E**。（Go 侧对应实现为 `internal/drm/mp4decrypt.go` 的 `FindMp4decrypt`，未在本片核对。）
- **RF-86（`DownloadTask.Snapshot()` 锁外读 Status/IsSuccessful）**：serve 任务快照 → **A**。（Go 对应 `internal/server/server.go`。）
- **RF-28 / RF-51 / RF-53 / RF-79 / RF-80（响应体有界读取、异常消息净化）**：HTTP/Core → **D/E**。本片仅确认这些类型在 Go 中不构成"整批中止"（#35）。
- **RF-54 / RF-70（日志注入、`SanitizeLogString`）**：serve/CLI 日志 → **A**。
- **RF-56 / RF-82（serve `area` 白名单、FilePattern 不变量）**：serve 选项 → **A**。
- **RF-61（`--save-archives-to-file` 产物路径文档）**：文档 + `APP_DIR` 语义。Go 侧 `BBDown.archives` 同样写在 `util.ExecutableDir()`（`internal/workflow/workflow.go:1461`、`1478`），与上游一致 → 文档条目，**D**。
- **RF-16 / RF-39~42 / RF-71 / RF-84 / RF-85（文档族）**：**D**。
- **v1.6.15「媒体下载切换至禁用自动跳转的 `MediaDownloadClient`」**：HTTP 客户端收口 → **D**。（顺带指出：Go `internal/util/http.go:234-237` 的 `DownloadClient()` 每次都 Clone transport 且不设 Timeout，与 #13 相关。）

## 未能判定（❓）汇总

- **#33（RF-45）**：Go 侧无"免二压重发"链路，静态阅读无法判定这是"有意省略"还是"能力缺失"；需与 E 片结论合并后才能给出 ✅/❌。

## 建议的验证方式

1. **#15（ffmpeg 输出选项位置）——已实证，建议固化为回归测试**：本片用真实 ffmpeg（n9.0.1）复现，`-i in.mp4 -i cover.jpg -disposition:v:1 attached_pic -i chapters -map 0 -map 1 -map_chapters 2 -c:v copy -c:a copy -y out.mp4` → **exit 234**，`Option disposition:v:1 cannot be applied to input url chapters`；把输出选项移到 `-map` 之后 → exit 0。建议在 Go 侧加"参数顺序"单测（断言所有 `-metadata:*`/`-disposition:*` 位于最后一个 `-i` 之后），并加一条真实 ffmpeg 集成用例（带封面 + 章节 + 2 字幕）验证产物 stream metadata。
2. **#22（mp4box 丢配音轨）**：在装有 mp4box 的环境跑 `--use-mp4box` + 含背景音轨的视频，`MP4Box -info` 断言音轨条数与输入一致。
3. **#18（事务化混流缺失）**：构造混流中途被杀（`kill -9` ffmpeg）后重跑，观察是否因"savePath 存在且非空"被跳过并报告成功——预期当前 Go 会误判为已完成。
4. **#30 / #31（路径穿越）**：用本地假 API 服务器把 `dash.video[].id/width/frame_rate/dfn` 与 `page.aid/cid` 回填为 `..%2F..` 形式（或 `../../tmp/x`），以 `-F "<videoTitle>/<res>/<fid>"` 与默认模板跑一次，断言产物仍落在 `--work-dir` 内。
5. **#14（读停滞看门狗）**：本地起一个发完响应头即停发数据的 HTTP 服务，观察 `DownloadFile` 是否永久挂起（当前预期：挂起，需 Ctrl+C）。
6. **#36（入档粒度）**：3 分 P 稿件 + `--save-archives-to-file`，让 P2 失败，然后检查 `BBDown.archives` 是否已含该 aid 且下次运行全部跳过（当前预期：会）。
7. **#2 / #6 / #16 / #19（行为一致性）**：`--cover-only` 跑一个多 P 视频（应只产出封面，当前预期会下全量）；2 段 FLV 转封装故意让第 2 段失败（应无 `.ts` 残留）；多 P 模板 + `--comments`（应产出 comments.json）；`--sub-only` + ASS 源字幕（产物应为 `.ass`）。
8. **#7 / #27（探针）**：把 `FFMPEG` 指向一个"启动后 sleep 60"的假脚本 + `--skip-mux`，观察是否卡 60s（当前预期：会卡，且不必要地启动进程）。
