# 片 C：live / article / sub / watchlater / login

> 审计对象：Go 重写仓库（`/home/qc233/github-code/BBDown`，分支 `main`，Go 侧基线 = C# v1.6.11）
> 参照源：本地 git 对象库中的上游 `aliveranme/BBDown` `v1.6.19`（`2d2573b`），全程离线
> 条目来源：CHANGELOG `[1.6.12]`..`[1.6.19]` 中落在本范围的要点 + REVIEW_FINDINGS 相关 RF 条目
> 说明：上游是解决方案，本范围文件的真实路径为 `BBDown/Infrastructure/{LiveStreamUtil,ArticleUtil,SubscriptionStore,BBDownLoginUtil,ConsoleQRCode}.cs`、`BBDown/Commands/{Live,Sub,WatchLater,Article,Login,LoginTV}Command.cs`（已用 `git ls-tree` 核对）

## 结论摘要

- 审核条目数：**35**（✅ 8 / ⚠️ 5 / ❌ 17 / ➖ 5 / ❓ 0）
- 最高风险 3 条：
  1. **直播录制的“已录内容”可被静默删除（P0 数据丢失簇）**：`internal/live/live.go:124` 的 `defer os.RemoveAll(segRoot)` 在**所有**返回路径上递归删掉整个 `.segs`（含历次保留分段），合成失败的日志却写着“分段保留在 sessionDir”；叠加 `live.go:156-161` 在读中断时先删当前分段再判取消，Ctrl+C 会丢掉整段已录字节。
  2. **登录凭据可被 3xx 引向任意主机（P0 安全）**：WEB 扫码轮询（`internal/login/login.go:69`）与 TV 登录两个端点（`login.go:149`、`:191`）全部走自动跟随重定向的共享客户端（`internal/util/http.go:111`、`:251`），无逐跳可信主机校验、无 3xx 拦截，SESSDATA 与轮询下发的 access_token 都在外发面内。
  3. **直播录制可永久挂死且无重连兜底（P0 可用性）**：流读取用 `http.DefaultClient`（Timeout=0）+ 阻塞读，**无 60s 读停滞看门狗**（`live.go:250`、`:266-282`）；同时重连被硬限 3 次（`live.go:20`），与上游“不设上限、网络恢复自动续录”的承诺相悖。
- 本片整体判断：**live 是绝对主战场且基本未对齐**——上游该文件 +467/−107，Go 侧 `internal/live/live.go` 仅 316 行，v1.6.12/v1.6.13 的录制韧性整批（凭据与 qn、取消保留、看门狗、无限重连、尾裁剪、大小校验、三态结果、`.segs` 清理）**一条都没落地**。login 侧是第二个高危面（重定向收口整族缺失 + `code==0`/token 校验缺失）。article/sub/watchlater 的“契约类”条目（损坏中止、单条隔离、取消退出码）反而**已对齐甚至更宽**，真实缺口集中在 `--work-dir`、保留名净化、sub check 失败计数与 `--work-dir`/`--cookie` 一类可观测行为上。

## 条目明细

| # | 来源 | 上游变更 | Go 现状 | 判定 | 证据（Go 侧 file:line） | 建议动作 | 优先级 |
|---|---|---|---|---|---|---|---|
| 1 | v1.6.12 | 直播分段合并改 staging 临时文件，并在合并后校验产物大小（多段时 `outLen < totalInputBytes*0.8` 且差 > 64KB 判定为截断失败） | ffmpeg 直接以最终路径为输出，无 staging、无大小校验；只要 `cmd.Run()` 返回 nil 就当作合成成功，坏段导致的截断产物会被静默接受 | ❌ | internal/live/live.go:230-239（cmd 直接写 path，仅判 Run 的 err） | 输出到 `path.concat-*.tmp.flv`，成功后比对各段总字节（0.8 阈值 + 64KB 容差）再 rename；失败即返回“保留分段”态 | P0 |
| 2 | v1.6.12 | 引入 `LiveRecordResult` 三态（Success / ConcatFailedWithSegmentsSaved / NoData）供调用方区分 | `DownloadToFile` 返回 `(bool, error)`，只有“有无数据”两态；合成失败只以 error 表达，调用方无法区分“可恢复分段已保留” | ❌ | internal/live/live.go:118（签名）、:202-203、:237；internal/cli/commands.go:138-147 | 定义三态并在 live 命令分支处理（NoData → 退 1；ConcatFailed → 提示分段位置并退 1） | P0 |
| 3 | v1.6.13 | 终结态清理：成功或未录到任何分段才清理本次会话目录；合成失败保留分段；仅当 `.segs` 根已空才删根；旧会话只提示不删 | `defer os.RemoveAll(segRoot)` 在函数入口注册，任何返回路径（合成失败、Ctrl+C、重连耗尽）都会递归删除整个 `.segs`，含历次保留的可恢复分段 | ❌ | internal/live/live.go:119-124（defer 位于录制开始前）、:237（日志谎称“分段保留在 sessionDir”） | 只删本次 session 目录；根目录仅在为空时删；失败路径不删；启动时扫描旧会话并提示路径 | P0 |
| 4 | v1.6.13 | `live` 录制前加载登录凭据：`--cookie` 显式优先，否则读本地 `BBDown.data`；新增 `--cookie`/`--access-token`；登录检测超时安全降级 | 命令用 `buildHTTPClient(config.MyOption{})`，既不调 `InitSession` 也不调 `loadCredentials`；`--cookie`/`--access-token` 是全局 persistent flag 故显式传入可用（Cookie 会随请求发出），但本地 `BBDown.data` **不会被读取** | ⚠️ | internal/cli/commands.go:124；internal/cli/root.go:205-206；internal/util/http.go:89-98；internal/workflow/workflow.go:1025-1045 | live 命令改为走 `workflow.InitSession`（或至少 `loadCredentials`），并打印登录状态 | P1 |
| 5 | v1.6.13 | 画质请求 qn=10000 → qn=30000（按账号权限回落）；接口只列 ts/fmp4 时自动回退 qn=10000 再取一次；启动时打印实际解析到的画质 | URL 硬编码 `qn=10000`；无任何回落重取；解析结构体里根本没有 quality/qn 字段，也没有画质名输出 | ❌ | internal/live/live.go:58-61；internal/live/live.go:67-113（无 quality 字段） | 改 qn=30000，flv 缺失时以 qn=10000 重试一次；解析 quality 并打印 | P1 |
| 6 | v1.6.13 | Ctrl+C 发生在分段读写阶段时，已写字节照常返回并计入合成（旧实现整段丢弃） | `streamToFile` 已把已写字节数 `offset` 随错误返回，但调用方在 `err != nil` 分支**先删分段再判取消** → 录制数分钟后 Ctrl+C 会丢掉全部内容 | ❌ | internal/live/live.go:156-161（先 os.Remove 再 if ctx.Err）；internal/live/live.go:269-283（offset 已返回） | 段字节 ≥13 才保留并计入 total；取消时 break 后照常走合成 | P0 |
| 7 | v1.6.13 | 读停滞看门狗：每收到数据重置 60s 计时，超时按读中断自动重连（网络黑洞不再永久挂起） | 流请求用 `http.DefaultClient`（Timeout 为 0）+ 阻塞式 `resp.Body.Read`，无任何停滞计时 → 连接不复位也不 EOF 时读取永久挂起、录制卡死 | ❌ | internal/live/live.go:250；internal/live/live.go:266-282 | 加可注入的 60s idle 计时（读取有数据即重置），超时按读中断处理 | P0 |
| 8 | v1.6.13 | 不限次重连：只要房间仍在播且用户未取消就持续指数退避（3s→6s→…→30s 封顶），网络恢复自动续录 | `reconnectLimit = 3` 硬上限；退避是线性 `3s×n` 且 15s 即封顶；达上限直接报错退出 | ❌ | internal/live/live.go:20、:139、:163；internal/live/live.go:143-146、:167-170 | 去掉重连上限，退避改 `3s<<n` 封顶 30s；只有取消与终结态退出循环 | P1 |
| 9 | v1.6.13 | 合成/改名前把每个分段裁到最后一个完整 FLV 标签（含 Filter 标志掩码 0x1F 识别），否则 concat demuxer 遇半截标签中止整场合成 | 无任何裁剪逻辑，原始分段直接写进 concat 列表 → 断流重连后的录制几乎必然合成失败 | ❌ | internal/live/live.go:218-238（直接 concat）；全仓无 TrimFlvTail（grep 无命中） | 实现 FLV 逐标签扫描，合成前就地截断尾标签 | P1 |
| 10 | v1.6.13 | 终结态区分：直播间不提供 flv（仅 HLS/ts）与本地写盘失败不再无限重试，直接报错并保留已录分段 | 无 flv 时错误文本为“无法获取直播间 … 的可录制流地址”，未命中“当前未在直播”判定 → 落入可重试分支；`os.Create` 写盘失败同样按瞬态故障重试 | ❌ | internal/live/live.go:112 + :132-142；internal/live/live.go:260-263 + :156-176 | 引入终结态错误类型（NoFlv / 写盘失败）直达失败分支，不进退避重连 | P1 |
| 11 | v1.6.18 / RF-46 | live 接口 code=0 但缺 data/playurl_info 节点时不再抛 KeyNotFoundException 终止整场，改按瞬态故障走既有退避重连 | Go 用结构体解码，缺节点只得到零值、不会 panic，最终以“无法获取…可录制流地址”返回并进入重连分支（这一点已对齐）；但仍受 #8 的 3 次上限约束，达不到“不设上限、恢复后续录” | ⚠️ | internal/live/live.go:67-112（零值解码，无 panic）；internal/live/live.go:132-142（重试上限） | 随 #8 去掉重连上限即等价；可补“畸形响应”单测 | P1 |
| 12 | v1.6.14 | 直播分段合成捕获进程启动异常（Win32Exception），不当作崩溃 | Go 中启动失败就是普通 error：`cmd.Run()` 返回 err → 走合成失败分支退出 1；且命令侧已前置校验 ffmpeg 存在且可执行（LookPath） | ✅ | internal/live/live.go:232-238；internal/cli/commands.go:113-119 | 无需动作（C# 异常类型体系差异由 Go error 模型消化） | P2 |
| 13 | v1.6.16 | `article`/`live` 子命令支持 `--work-dir`：默认产物与 `.segs` 落 workDir，相对 `--output` 也相对 workDir 解析 | 全局 persistent `--work-dir` 存在，但 article/live 分支完全不读 `optWorkDir`（只有 watchlater/sub check 使用）→ 用户传了也静默无效 | ❌ | internal/cli/commands.go:132-135（live）、:171-174（article）、:257/:367（仅这两处用 optWorkDir）；internal/cli/root.go:214 | 两命令解析 workDir 并据此拼接默认/相对输出路径（含 `.segs` 派生） | P1 |
| 14 | v1.6.18 / RF-36 | 专栏与直播文件名净化接入 Windows 保留名防护（与下载管线 `GetValidFileName` 同规则） | article 与 live 默认文件名都用 `live.SanitizeFileName`（只替换非法字符，无保留名防护）；项目已具备 `util.GetValidFileName`（含 reservedNames）却未被使用 | ❌ | internal/cli/commands.go:173、:134；internal/live/live.go:286-307；internal/util/file.go:16-22、:34-70 | 两处改调 `util.GetValidFileName(title, "", true)` | P1 |
| 15 | v1.6.18 | 评论 JSON / 专栏 Markdown 时间戳固定 InvariantCulture（fi-FI 下 `:` 被当时间分隔符而产生跨机漂移） | Go 的 `time.Time.Format` 使用显式 layout，天然与区域设置无关；`time.Unix().Local()` 与上游 `LocalDateTime` 语义一致 | ➖ | internal/article/article.go:79-80 | 无需动作（C# culture 敏感特有） | P2 |
| 16 | ArticleUtil.cs 实现差异（CHANGELOG/RF 未列） | `GetPropertySafe("code").GetInt32()` → `GetInt32Safe("code")`，畸形 code 不再抛异常 | Go 用带类型结构体解码，code 非法（类型不符/越界）时返回可读的“解析专栏响应失败”，不会崩溃 | ✅ | internal/article/article.go:41-56 | 无需动作 | P2 |
| 17 | v1.6.18 / RF-30 | 订阅历史损坏被隔离后必须立即终止整个 check（否则后续 aid 会静默重建空历史、下次全量重下） | `CorruptError` 专用类型 + 损坏即 rename 隔离；`Load`/`LoadHistory`/`RecordDownloaded` 的错误在 runSubCheck 中一律 `return err` 直接上抛，不落入“单条失败继续” | ✅ | internal/substore/substore.go:22-27、:59-65、:143-147、:152-162；internal/cli/commands.go:349-352、:403-406、:441-443 | 无需动作；建议补“损坏后 sub check 必须非 0 退出且历史不被覆盖”的回归测试 | P0（保持） |
| 18 | v1.6.18 / RF-32 | `sub check` 主动取消（Ctrl+C）返回 0（与 watchlater 及文档契约一致） | 循环顶部 `ctx.Err() != nil` → `silenceOnCancel(ctx.Err())` 返回 context.Canceled → `Execute` 统一 `os.Exit(0)` | ✅ | internal/cli/commands.go:377-380；internal/cli/root.go:135-141、:147-155 | 无需动作；可选差异：取消后当前订阅剩余 aid 仍会各打一条“下载失败”告警（上游立即重抛） | P1 |
| 19 | v1.6.18 / RF-32、v1.6.12 | token 未取消的中断/单条超时按失败返回 1；单条 aid 失败计入 failedSubs，有失败即非 0 退出 | 逐 aid 只 `LogWarn + continue`，**没有任何失败计数**，函数末尾恒 `return nil` → 全部失败、单条超时、部分失败都以退出码 0 结束，脚本/CI 无法区分 | ❌ | internal/cli/commands.go:437-440（无计数）、:446（无条件 return nil） | 增加失败计数（含取消未标记的错误路径），有失败即返回非 0，与 watchlater 的 `failed>0 → error` 对齐 | P1 |
| 20 | v1.6.12 / v1.6.18 / RF-44 / RF-72 | sub/watchlater 单条超时、取消、权限错误、畸形数据不得中止整批 | Go 无异常类型白名单：sub 逐 aid 任意 error 都 continue；watchlater 逐条 `failed++/continue`，仅 `context.Canceled` 立即返回取消 | ✅ | internal/cli/commands.go:437-440、:297-304 | 无需动作（隔离面是 C# 白名单的超集） | P1 |
| 21 | v1.6.18 / RF-44、v1.6.19 / RF-72 | 两级与命令级失败隔离过滤器补齐 UnauthorizedAccessException / InvalidDataException | Go 无异常类型体系，也不存在“类型白名单漏网”这一缺陷面，行为面由 #20 覆盖 | ➖ | internal/cli/commands.go:437-440（无条件隔离任意 error） | 无需动作（C# 异常类型体系特有）；真实缺口只有失败计数（见 #19） | P2 |
| 22 | v1.6.15（CHANGELOG 未列，df4eba9） | SubscriptionStore：历史每 target 上限 5000 条截断；RecordDownloaded 改 Remove+Add（移末尾，保“最近”语义）；AtomicWrite 自动创建父目录 | 无上限（多年部署无界增长、每次 check 全量解析变慢）；已存在即 `return nil`，不做重排；`atomicWrite` 不建父目录 | ⚠️ | internal/substore/substore.go:152-175（无上限、:167-172 不重排）、:42-56（无 MkdirAll） | 加每 target 上限 5000 与“移到末尾”语义；写前 MkdirAll | P2 |
| 23 | v1.6.19 / RF-70 | `watchlater`/`live` 的服务器可控 title/uname 过 `SanitizeLogString`，防 CRLF 伪造日志行 | 全仓无该净化函数（grep 无命中，logger 只有 Log/LogWarn/LogDebug）；live 打印 title/uname、watchlater 打印条目 title 均为原文 | ❌ | internal/cli/commands.go:131、:285；internal/util/logger.go:51-114（无净化） | 在 util 增加单行化 + 截断的 `SanitizeLogString`，两处调用 | P2 |
| 24 | v1.6.12 / v1.6.18 | watchlater 单条隔离 + 取消返回 0 / 非取消中断返回 1；列表接口 data 节点缺失安全 | 逐条 `failed++/continue`（仅 context.Canceled 立即返回取消）；`failed>0` 返回 error → 退出 1；fetchWatchLater 用结构体解码，data 缺失为安全零值 | ✅ | internal/cli/commands.go:297-310、:319-345 | 无需动作 | P2 |
| 25 | v1.6.17 / RF-13 | WEB 登录轮询（携凭据且响应 Set-Cookie 是新凭证下发通道）改禁自动跳转客户端手动逐跳，每跳校验 `IsTrustedCookieHost` | `login.LoginWeb` 调 `GetWebSourceWithSetCookies`，其内部用默认 `c.client.Do`，自动跟随任意 3xx 且不校验目标主机 → SESSDATA 与下发的 Set-Cookie 可被引向任意主机 | ❌ | internal/login/login.go:69；internal/util/http.go:111（无 CheckRedirect；全仓仅 internal/server/server.go:562 有） | 用 `http.Client{CheckRedirect: ErrUseLastResponse}` 手动逐跳 + 官方域名白名单校验 | P0 |
| 26 | v1.6.18 / RF-37 | TV 登录 auth_code 获取与扫码轮询改禁跳转客户端 + 3xx 显式拦截（POST 体含 appsecret 签名、轮询响应携 access_token） | 两处都走 `client.PostForm` → 同一自动跟随客户端，无 3xx 拦截（响应释放已由 defer 覆盖，这点已对齐） | ❌ | internal/login/login.go:149、:191；internal/util/http.go:242-260（:251 Do、:255 defer Close） | 两端点改用禁跳转客户端并把 3xx 视为确定性失败 | P0 |
| 27 | v1.6.18 / RF-59 | 登录轮询遇 3xx 无 Location 时按终态读 body 返回，不再误报“重定向跳数超限” | Go 完全没有手动逐跳实现，故该误报不存在；但也说明逐跳防线整体缺失（见 #25/#26），属于“同根缺失”而非“已对齐” | ⚠️ | internal/util/http.go:111（无逐跳代码）；internal/login/login.go:69 | 随 #25/#26 一并实现，并在无 Location 分支按终态处理 | P2 |
| 28 | v1.6.18（v1.6.19 / RF-66 再改回） | TV 登录两请求改用禁跳转客户端池，客户端超时随之 2min→1min；1.6.19 RF-66 又把该池统一为 2min | Go 单客户端 2 分钟总超时、无客户端池分层；上游最终值同为 2 分钟，用户可见行为等价 | ➖ | internal/util/http.go:35-39；internal/login/login.go:149、:191 | 无需动作（C# 客户端池划分特有；RF-66 本体归 D） | P2 |
| 29 | v1.6.12 | 扫码登录严格校验：顶层 `code != 0` 报错返回、`data` 节点缺失报错、只有 `data.code == 0` 才算成功、TV `access_token` 为空拒绝落盘 | WEB：`switch pollResult.Data.Code` 的 `default` 分支即视为登录成功，顶层 `code` 字段解出后从未使用，未知非 0 码也会按成功处理；TV：`default` 分支直接取 token 写盘，**空 token 也会写出 `access_token=`** 到 `BBDownTV.data` | ❌ | internal/login/login.go:79-110（:79-80 顶层 Code 定义、grep 全文件无 `pollResult.Code` 引用；:104 default=success）、:200-231（:221-226 空 token 落盘） | 补顶层 code 与 data 存在性校验、`case 0` 成功、未知码报错返回；TV 侧校验 token 非空后再写盘 | P1 |
| 30 | v1.6.14、v1.6.12 | 登录轮询 GET 加入有界重试 + 指数退避；HTTP 内部超时不再被当作主动取消（超时→失败非 0，主动取消→0） | 轮询错误一律 `LogWarn + continue`（每轮固定 sleep 1s），**无退避也无重试上限**；取消语义正确：ctx.Err() 时返回 context.Canceled → 统一 exit 0，`http.Client.Timeout` 的超时不满足 `context.Canceled` → exit 1 | ⚠️ | internal/login/login.go:66-77、:183-198；internal/cli/root.go:135-141；internal/util/http.go:36-39 | 加指数退避与试次上限；超时单独提示（区分网络失败与二维码过期） | P1 |
| 31 | v1.6.19 / RF-79 | 登录（及 DRM）响应体读取改有界（64MB） | 全仓无任何 body 上限：WEB 登录轮询/二维码生成走 `io.ReadAll`，TV 端点的 `PostForm` 同样无上限 | ❌ | internal/util/http.go:122、:260；`grep LimitReader/MaxBody` 全仓无命中 | HTTP 层统一有界读取（64MB），登录路径自动受益（同族 RF-28/RF-51 归 D） | P2 |
| 32 | v1.6.12（d54f06e，CHANGELOG 未列） | ConsoleQRCode 的前景/背景色恢复包 try/finally，异常时不再残留颜色 | Go 逐格直接输出 ANSI 转义序列、从不修改全局控制台颜色状态，不存在“异常后颜色残留”的状态需要恢复 | ➖ | internal/util/qrcode.go:31-41 | 无需动作（C# 全局 Console 颜色状态特有） | P2 |
| 33 | v1.6.12 | 二维码 PNG 写盘失败降级（仅 debug 记录，继续打印控制台二维码）；缩放倍数提取为常量（7） | PNG 写盘失败仅 stderr 提示后继续，终端二维码照常打印；调用方 `LogWarn` 后继续；缩放倍数同为 7 | ✅ | internal/util/qrcode.go:15-30（:20-23 降级）；internal/login/login.go:56-58、:170-172 | 无需动作；建议补“CWD 不可写时登录仍可完成”的用例 | P2 |
| 34 | v1.6.17 / RF-2 | CLI 命令层全量迁移 `AsyncCommand`（login/logintv/article/live/sub check/watchlater/serve 不再 `Task.Run` 阻塞线程池） | Go 无 async-over-sync：命令即 `RunE` + context，不存在阻塞等待异步操作的问题 | ➖ | internal/cli/commands.go:43-56（login）、:103-149（live）、:348-448（sub check） | 无需动作（C# async-over-sync 特有） | P2 |
| 35 | v1.6.12 | `GetWebSourceWithSetCookiesAsync` 补齐整体超时（登录轮询路径），并修复 4xx/5xx 时响应未释放的连接泄漏 | Go 单客户端 `Timeout: 2 * time.Minute` 覆盖整请求；`defer resp.Body.Close()` 位于状态码判定之前，无泄漏窗口 | ✅ | internal/util/http.go:35-39、:111-120 | 无需动作 | P2 |

## 归属其它范围的条目

- RF-43 / RF-44（进程启动 Win32Exception、UnauthorizedAccessException 过滤器补齐）→ 主体在下载管线与外部程序：**B**
- RF-57（ToolFinder 在 CWD 搜索 mp4decrypt/device.wvd）→ **B**
- RF-45 / RF-47 / RF-48 / RF-49 / RF-52 / RF-53 / RF-60 / RF-63 / RF-65 / RF-80（fetcher / parser / 格式化）→ **E**
- RF-62 / RF-78 / RF-88（Widevine / WvdDevice 及其测试）→ **E**
- RF-64（评论保存 catch 白名单）→ **E**（评论与弹幕字幕同片）
- RF-50 / RF-51 / RF-66 / RF-28（HTTPUtil 客户端池、逐跳收口、有界响应体）→ **D**（本片 #25/#26/#31 与其同根，修复应在 HTTP 层统一落）
- RF-54 / RF-55 / RF-56 / RF-74 / RF-81 / RF-82 / RF-83 / RF-86 / RF-9 / RF-10 / RF-15 / RF-24 / RF-38（serve 日志、Host/Origin、限速、快照锁）→ **A**
- RF-58 / RF-73 / RF-7 / RF-82（路径净化、配置合并 URL 启发式）→ **B / D**
- RF-67 / RF-68 / RF-69 / RF-75 / RF-76 / RF-77 / RF-87（CI 与假绿测试）→ 工程范围，非本片
- RF-61 / RF-71 / RF-84 / RF-85（文档族：archives/占位符/API.md/Docker）→ 文档范围
- 1.6.17「直播杜比视界的 ffmpeg 版本探测改真异步」、1.6.14「混流轨道清理兜底 / aria2c 超时」、RF-1 分片扩展名一致性 → **B**（下载与混流管线）
- 1.6.15 新增 `BBDown.Core/Util/ServerClock.cs`（时钟校准收窄至 api.bilibili.com、±1h）→ **D**

## 未能判定（❓）汇总

- 本片 **无 ❓ 条目**：35 条均可用静态阅读定论（Go 侧代码路径短且判定分支显式，见各行证据）。
- 需要说明的“静态可判但建议运行复核”之处：#6（Ctrl+C 保留内容）、#7（读停滞看门狗）、#18/#19（退出码）依赖真实信号与网络行为，静态结论成立但值得用集成测试固化。

## 建议的验证方式

1. **编译与现有测试**：`go build ./... && go test ./internal/live/... ./internal/article/... ./internal/substore/... ./internal/login/... ./internal/cli/...`（确认基线绿，便于后续改动回归）。
2. **直播录制集成（本地假 B 站服务器）**，建议断言：
   - Ctrl+C 后产物字节 > 0 且最终文件存在（#6）；
   - 连续断流 ≥4 次仍继续录制（#8）、读停滞 60s 后重连（#7）；
   - live 响应缺 `data/playurl_info` 时进入退避而非终止（#11）；
   - 仅 HLS/ts（无 flv）时立即失败且分段保留（#10）；
   - 人为构造“完整标签 + 截断尾”的分段后 concat 成功（#9）；
   - 合成产物 < 80% 输入时判失败且 `.segs` 完整保留（#1/#3）。
3. **登录安全回归（httptest 假 passport）**：WEB 轮询与 TV 两端点返回 302 到非可信主机 → 断言不跟随且报错；poll 返回顶层 `code!=0`、`data` 缺失、`data.code` 未知非 0、TV `access_token` 为空 → 断言不落盘（#25/#26/#29）。
4. **sub check 契约**：损坏 history 后运行必须非 0 且历史不被覆盖（#17）；单条下载失败必须非 0（#19）；Ctrl+C 返回 0（#18）。
5. **日志注入**：构造含 `\r\n` 的直播间标题/稍后再看标题，断言日志单行（#23）。
6. **路径与目录**：`article`/`live` 带 `--work-dir` 时产物落在该目录（#13）；标题为 `CON`/`NUL` 时产物可落盘（#14）。
