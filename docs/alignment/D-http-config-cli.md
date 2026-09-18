# 片 D：HTTP 传输 / 凭据 / URL 解析 / 配置 / CLI 层

> 上游基线 v1.6.11 → 目标 v1.6.19（111 commits / 8 releases）。上游源码真身在 `BBDown.Core/Util/HTTPUtil.cs`（+686/−122）、`BBDown.Core/{Logger,AppHelper,Config,AppSettings}.cs`、`BBDown.Core/Util/{PathUtil,JsonElementExtensions,SensitiveDataMasker}.cs`、`BBDown/Utilities/{BBDownUtil,UrlResolver}.cs`、`BBDown/Configuration/{BBDownConfigParser,MyOption}.cs`、`BBDown/Program.cs`。本文所有结论均附 Go 侧 `file:line` 证据；两条依赖运行时语义的判据已用本机 Go 1.27 探针实测（见文末）。

## 结论摘要

- 审核条目数：34（✅ 8 / ⚠️ 8 / ❌ 14 / ➖ 4 / ❓ 0）
- 最高风险 3 条：
  1. **携凭据外发收口整体缺失**（❌ #7/#8/#10/#11）：发送前无可信 host 校验、cookie 客户端仍自动跟随重定向（仅靠 Go 标准库跨域剥离 Header 兜底）、gRPC POST 与 TV 登录 POST 均未拦 3xx。
  2. **配置合并 `cliHasURL` 误判未修**（❌ #19）：命令行任何 URL 形值（`--aria2c-proxy http://...`、`--work-dir av123`）都会压掉 `BBDown.config` 里的 URL，导致 Spectre/绑定层报缺少参数——上游 RF-7 已修，Go 仍为旧逻辑。
  3. **文件名尾随点/空格未裁剪**（❌ #26）：`video.` / `video ` 类标题在 Windows 上落盘直接失败（上游 v1.6.14 已修，Go 仍是旧实现）。
- 次高风险（P1）：API 层无任何有界重试/指数退避（❌ #4，上游 v1.6.14 起统一重试语义）；服务器时钟校准整体缺失（❌ #6，本地时钟偏差超 60s 时 WBI 签名被拒）；响应体 64MB 上限与 gzip 48MB 上限缺失（❌ #12/#13，内存耗尽面）。
- 本片整体判断：**Go 侧在「超时矩阵 / 连接释放 / 取消与超时分类 / SSL 策略隔离」这些不变量上已天然对齐**（Go 单客户端 2 分钟超时覆盖含响应体的全阶段，`Client.Timeout` 错误是 `DeadlineExceeded` 而非 `Canceled`，`defer Close` 在状态码检查之前）；但**「凭据外发纵深防御」整族（RF-13/50 + v1.6.15/1.6.18）、「有界读取」「重试/退避」「时钟校准」「日志单行化」几乎全部缺失**，属上游第 9~16 轮审查的主攻方向，Go 侧尚未跟进。配置与 CLI 层有两处确定性缺陷（RF-7、退出码 130 契约）与一处 Windows 文件名缺陷。

## 条目明细

| # | 来源 | 上游变更 | Go 现状 | 判定 | 证据（Go 侧 file:line） | 建议动作 | 优先级 |
|---|---|---|---|---|---|---|---|
| 1 | v1.6.12 | HTTPUtil 内部超时 CTS 耗尽后转 `TimeoutException`，避免被 CLI 顶层误判为用户取消（退出码 130） | Go 无需转换即天然成立：`http.Client.Timeout` 的错误是 `context.DeadlineExceeded`，`errors.Is(err, context.Canceled)` 为 false，不会命中取消分支 | ✅ | `internal/util/http.go:36-39`；`internal/cli/root.go:138` 仅对 `context.Canceled` 特判；探针实测 `Is(Canceled)=false / Is(DeadlineExceeded)=true` | 无需改动；可在注释中固化该不变量 | P2 |
| 2 | v1.6.12 | 为 `GetWebSourceWithSetCookiesAsync`/`GetWebSourceAnonymousCheckedAsync` 补齐 `ApiTimeoutMs` 整体超时（.NET 的 ResponseHeadersRead 后 HttpClient.Timeout 不再约束响应体） | Go `Client.Timeout` 覆盖「建连+跳转+读取响应体」全阶段，所有请求（含 Set-Cookie 路径）都已受 2 分钟约束 | ✅ | `internal/util/http.go:36-39`、`:111`、`:122`（同一 client 读 body） | 无需改动 | P2 |
| 3 | v1.6.12 | 修复 `using` 声明在 `EnsureSuccessStatusCode()` 之前导致 4xx/5xx 连接未释放 | Go 三处均为 `defer resp.Body.Close()` 先于状态码检查，无泄漏路径 | ✅ | `internal/util/http.go:115-120`、`:197-201`、`:255-259` | 无需改动 | P2 |
| 4 | v1.6.14 / v1.6.17 | HTTP 重试与超时语义：API 层对 5xx/传输层失败/超时有界重试（`min(MaxRetryCount,3)`）+ 指数退避（含登录轮询 GET 从零重试补齐） | Go 传输层零重试：非 2xx 直接返回错误；`RetryCount/RetryDelay` 只用于**页面级**重试，瞬时 5xx/超时立即冒泡到页面级重试（放大为整页重下） | ❌ | `internal/util/http.go:111-135`（无 attempt 循环）、`:193-204`、`:251-260`；仅 `internal/workflow/workflow.go:357-360`（页面级） | 在 `HTTPClient` 增加统一 `doWithRetry`（5xx + 网络错误 + DeadlineExceeded，最多 3 次，指数退避封顶 12s），4xx 不重试 | P1 |
| 5 | v1.6.14 / RF-51 | 响应体读取 + charset 解码（含 BOM 剥离对齐），风控 HTML 识别双剥 BOM | Go 直接 `io.ReadAll` → `string(body)`：无 charset 解码（非 UTF-8 页面乱码）、无 BOM 剥离（`\ufeff{...}` 会让 `encoding/json` 报 invalid character） | ❌ | `internal/util/http.go:122`、`:127`；全库无 BOM/charset 处理（grep `ufeff` / `BOM` 0 命中） | 增加 `decodeBody([]byte, contentType) string`：按 charset 解码 + 剥离前导 BOM，统一供各读取点调用 | P2 |
| 6 | v1.6.14 / v1.6.15 | 服务器时钟校准：从响应头 `Date` 校准偏移；仅 `api.bilibili.com`（含子域）可写全局偏移；阈值 ±1h；`--insecure` 链路的 Date 不参与校准 | Go 完全没有时钟校准：无 `Date` 头解析、无偏移状态，WBI 签名取本地时间。本地时钟偏差 >60s 时 wts 超时效窗口被拒（上游新增此机制正是为此） | ❌ | 全库 `ServerClock` / `clockOffset` / `Calibrate` / `Header.Get("Date")` 均 0 命中；签名侧 `internal/util/wbi.go:11-14`（无时间补偿） | 在 `AppSettings` 加偏移字段 + 在 `HTTPClient` 响应处校准（仅权威主机、±1h、insecure 跳校准），签名时间戳读校准值 | P1 |
| 7 | v1.6.15 / RF-50 | 携 Cookie 请求发送前强制校验目标为官方域名或 `--host/--ep-host/--tv-host` 显式配置主机，拦截非可信凭据外发 | Go 只要 `cookieFn` 非空就无条件附加 `Cookie`，无任何 host 白名单检查（`IsTrustedCookieHost` 等价物不存在） | ❌ | `internal/util/http.go:89-98`（无 host 校验）；grep `IsTrustedCookieHost` / `trustedCookie` 0 命中 | 抽 `IsTrustedCookieHost(host)`（官方后缀 + 三个配置 host）并在 `GetWebSource*` 附加 Cookie 前断言；非可信时可回退匿名 | P1 |
| 8 | v1.6.18 / v1.6.19 / RF-13 / RF-50 | 携凭据 GET（`GetWebSourceCoreAsync(sendCookie:true)`）与登录轮询 `GetWebSourceWithSetCookiesAsync` 改禁跳转客户端手动逐跳，每跳过 `IsTrustedCookieHost`，上限 10 跳 | Go 两者共用自动跳转 client（未设 `CheckRedirect`）。标准库仅在**跨主机名**跳转时剥离 `Cookie/Authorization`（探针实测），同主机不同端口仍转发；且跳转目标的响应体与 `Set-Cookie`（登录新凭证下发通道）会被当作本请求凭证读取 | ⚠️ | `internal/util/http.go:36-39`（无 CheckRedirect）、`:89-98`、`:135`（Set-Cookie 来自终态响应）；探针实测剥离行为 | 为携凭据路径引入 `noRedirectClient` + 手动逐跳 + 每跳可信校验（与上游同构） | P1 |
| 9 | v1.6.17 / RF-59 | 登录轮询 3xx 无 Location 不再误报「重定向跳数超限」，按终态读 body 返回 | Go 无手动逐跳循环：标准库对无 Location 的 3xx 原样返回响应（探针实测 status=300），随后落到 `:118` 的非 2xx 分支报 `HTTP 300` 错误，登录轮询把确定性失败当瞬时故障继续轮询 | ⚠️ | `internal/util/http.go:111-120`；探针实测 `no-location 3xx: err=<nil> status=300` | 随 #8 的手动逐跳循环一并处理「无 Location 即按终态读 body」分支 | P2 |
| 10 | v1.6.15 | gRPC POST 拦截 3xx：禁用自动跟随，避免 `authorization`/`x-bili-metadata-bin`（含 access_token）随 307/308 重放跨主机 | Go `PostResponse` 走同一自动跳转 client；`appapi` 的唯一 gRPC 出口就是它，3xx 会被自动跟随（Go 标准库跨域剥 `Authorization`，但 body 仍会重放、同域不剥） | ❌ | `internal/util/http.go:193-201`（`c.client.Do`）；`internal/appapi/appapi.go:108` | `PostResponse` 改用禁跳转 client + 显式拦截 3xx 报错 | P1 |
| 11 | v1.6.18 / RF-37 | TV 登录两个端点（auth_code 获取、扫码轮询）改禁跳转客户端 + 3xx 显式拦截（轮询响应是新 `access_token` 下发通道） | Go 两处均走 `PostForm` → 自动跳转 client，无 3xx 拦截：3xx 可把签名参数与轮询结果引向其它主机，且终态响应的 `access_token` 被直接采信 | ❌ | `internal/login/login.go:149`、`:191` → `internal/util/http.go:242-261`（`c.client.Do` 自动跳转） | 新增禁跳转 POST 辅助方法，TV 登录两处切换并在 3xx 时报错（与 #10 共用） | P1 |
| 12 | v1.6.17 / v1.6.19 / RF-28 / RF-51 / RF-79 | 响应体统一 64MB 有界读取（Content-Length 预检 + 逐块累计双拦截），覆盖普通响应体、DRM 许可证、登录轮询 | Go 5 处裸 `io.ReadAll`，无任何上限：被攻破端点或 `--insecure` 中间人可用巨包/分块慢发打满内存 | ❌ | `internal/util/http.go:122`、`:203`、`:260`；`internal/appapi/appapi.go:254`；`internal/drm/widevine.go:266` | 提供 `util.ReadBounded(rc, max)`（64MB，超限返回 `ErrBodyTooLarge`）替换全部 `io.ReadAll` | P1 |
| 13 | v1.6.15 | gzip 解压设 48MB 输出上限，防解压炸弹 | Go `gzipDecompress` 无限读取解压流 | ❌ | `internal/appapi/appapi.go:248-255` | 解压循环累计长度超 48MB 立即 abort（可复用 #12 的有界读） | P2 |
| 14 | v1.6.15 | gRPC 帧首字节合法性校验（合法值仅 0/1，其它显式报畸形） | Go `readMessage` 直接把非 1 的首字节当「未压缩」处理，畸形帧只在后续 protobuf 解析处报难定位错误 | ❌ | `internal/appapi/appapi.go:221-238`（`:229` 仅判 `first == 1`） | 补 `first != 0 && first != 1` 的显式报错 | P2 |
| 15 | v1.6.14 | 日志敏感信息脱敏：敏感键新增 `DedeUserID` | Go `sensitiveQueryKeys` 已含 `dedeuserid`（含 `__ckmd5` 变体），`MaskCookie` 同样覆盖 | ✅ | `internal/util/mask.go:23-33`、`:85` | 无需改动（`sign/x_sign/w_rid` 的 URL 签名脱敏属 E 片） | P2 |
| 16 | v1.6.19 / RF-66 | 禁跳转客户端超时由 1 分钟对齐到 2 分钟（与 `AppHttpClient`/`ApiTimeoutMs` 同一不变量） | Go 只有一个带超时的 client（2 分钟）服务全部 API 路径，不存在 1 分钟池，超时矩阵天然统一 | ✅ | `internal/util/http.go:36-39`、`:234-239`（下载专用 client 无整体超时，由 ctx 驱动） | 若按 #8/#10 新增禁跳转池，必须沿用 2 分钟 | P2 |
| 17 | v1.6.19 / RF-53 | `GetPropertySafe` 异常消息里的服务器可控「全部键名」清单剥离控制字符并截断前 8 个 | Go 无该 helper（全库 grep `GetPropertySafe` 与 `available keys` 均 0 命中）：JSON 走结构体反序列化，错误消息不含服务器键名清单，注入面不存在 | ➖ | 全库 0 命中；Go 侧 JSON 解码为 typed struct（如 `internal/cli/commands.go:325-334`） | 无需移植；若未来新增 map 型解码再补净化 | P2 |
| 18 | v1.6.19 / RF-54 | URL 拆解派生串（`aidOri`/fid/sid 等 query 值）在 `ResolveAsync` 返回前统一单行化，堵 CRLF 日志注入；RF-25 同族的原始 `option.Url` 日志一并收口 | Go `queryParam` 取到的是 URL 解码后的原值（`?fid=%0d%0a...` 可注入），`ResolveURL` 返回值被原样落日志；无任何单行化函数 | ❌ | `internal/workflow/resolve.go:137-148`、`:274-280`；落日志点 `internal/workflow/workflow.go:92`、`internal/cli/commands.go:381`；grep `SanitizeLogString` / `单行化` 0 命中 | 增加 `util.SanitizeLogString`（剔除 \\r\\n/控制字符 + 截断），在 `ResolveURL` 返回前与日志 sink 各收口一次 | P1 |
| 19 | v1.6.17 / RF-7 | 配置合并 `cliHasUrl` 只对**位置参数**应用 URL 启发式，避免 `--aria2c-proxy http://...`、`--work-dir av123` 等选项值压掉配置里的 URL | Go 仍对**全部 argv** 跑 `urlLikeToken`：`--work-dir av123`/URL 形选项值被误判为「命令行已给 URL」，`BBDown.config` 里的 URL 被丢弃 → cobra 报缺少参数 | ❌ | `internal/config/parser.go:88-94`（全量扫描）；对照同文件 `:20-39` `IsSubCommandInvocation` 已有「带值选项吞下一 token」的可复用逻辑 | 抽 `positionalTokens(args, aliasMap, boolFlags)`（复用 `:20-39` 的跳值逻辑），`cliHasURL` 只扫位置参数；补 `--aria2c-proxy http://x` 回归用例（`internal/config/parser_test.go:10-35` 当前无此用例） | P1 |
| 20 | v1.6.17 / RF-2 | CLI 命令层从 `Task.Run + GetAwaiter().GetResult()` 迁移到 `AsyncCommand`（消除线程池阻塞） | Go 无 async-over-sync 概念：所有子命令 `RunE` 直接接收 `ctx`（cobra 同步签名 + 显式取消信号），无阻塞线程池等价问题 | ➖ | `internal/cli/root.go:121`、`internal/cli/commands.go:46-100` | 无需移植 | P2 |
| 21 | v1.6.17 | 退出码契约（wiki 勘误）：主命令 Ctrl+C 返回 130、子命令取消返回 0、工具缺失归 1 | Go 对所有命令统一「取消 → 打印 Force Exit 并 `os.Exit(0)`」，主下载命令的 130 语义缺失；工具缺失归 1 已对齐 | ⚠️ | `internal/cli/root.go:135-144`（`:138` 取消统一 exit 0）；工具缺失 `internal/cli/commands.go:113-119` → `root.go:143` exit 1 | 主下载命令（`runDownload`）取消时返回 130，子命令保留 0；与上游文档契约对齐 | P1 |
| 22 | v1.6.19 | `SetExceptionHandler`：取消识别返回 130；非取消异常完整堆栈经 `Logger.LogStack` 落盘（不受 DebugLog 门控）；`TaskCanceledException` 给可读中文提示 | Go 只 `fmt.Fprintln(os.Stderr, err)`：无堆栈落盘（`util.Logger.SetLogFile` 定义但全库 0 调用点），超时错误直接以 Go 原生英文串（`context deadline exceeded (Client.Timeout exceeded...)`）展示 | ⚠️ | `internal/cli/root.go:142`；`internal/util/logger.go:24-29`（`SetLogFile` 定义但 0 调用点）、`:51-56`（无 `LogStack` 等价物） | 加 `util.LogStack(err)`（写日志文件，无文件时仅 debug）+ 对 `DeadlineExceeded` 给中文可读提示 | P2 |
| 23 | v1.6.12 差异（CHANGELOG 未列） | Logger 改进程内单例 `StreamWriter`（AutoFlush + FileShare.ReadWrite/Delete），写失败连续 5 次闭锁并按 30s 冷却自愈，外部 mv/删除后按长度检测重建 writer，serve 关停 `CloseFile` 释放句柄 | Go 每行 `OpenFile+Close`（天然无句柄泄漏与轮转问题，但高频日志下反复开合），写失败静默返回（无闭锁/best-effort 语义） | ⚠️ | `internal/util/logger.go:31-44` | 语义可接受；若 serve 日志量大，改为持久 writer + 失败闭锁（行为等价，仅性能） | P2 |
| 24 | v1.6.12 差异（CHANGELOG 未列） | UA 按流隔离（删除静态可变 `HTTPUtil.UserAgent`）；UA 版本池 80-110 → 130-150（整数主版本）；`sec-ch-ua` 改为仅 Chrome UA 且版本从已解析 UA 提取，保证指纹自洽 | Go 仍是随机浮点 UA 80-110（陈旧指纹即风控信号）且 `sec-ch-ua` 硬编码 `131`，与实际 UA 版本可能不一致；UA 隔离已按 client 实例成立（`SetUserAgent`/`--user-agent`） | ⚠️ | `internal/util/http.go:63-70`（`randomVersion(80,110)`）、`:103`（硬编码 131）、`:218-222`（每 client UA，隔离 ✅） | UA 池升至 130-150 整数主版本；`sec-ch-ua` 由正则提取 UA 主版本构造 | P2 |
| 25 | v1.6.12 差异（CHANGELOG 未列） | SSL 策略按池隔离：校验池与不安全池各自独立 `SocketsHttpHandler` 连接池，回调改为构造时固化（不再每次握手读 `Config.Current.SkipSslCheck`），避免 `--insecure` 建立的连接被复用给校验流 | Go `NewHTTPClient` 每次新建 `http.Transport` 并在构造时固化 `InsecureSkipVerify`，池天然不共享——比上游旧实现更严 | ✅ | `internal/util/http.go:26-33`、`:47`（构造时读取 `skipSSL()`，无逐次回调） | 无需改动；注意不要改成共享全局 Transport | P2 |
| 26 | v1.6.14 | 文件名尾随点/空格裁剪（Windows 拒绝以点/空格结尾的名称）+ 纯点串兜底产出合法基名 | Go `GetValidFileName` 只做非法字符替换 + 保留名前缀，**不裁剪尾随点/空格**、无纯点串兜底：标题 `video.`/`video `/`...` 在 Windows 上创建失败 | ❌ | `internal/util/file.go:35-69`（`:57-67` 无 `TrimEnd('.',' ')`） | 在保留名处理之后补 `TrimEnd('.',' ')`；裁剪后为空则返回 `"_"` | P1 |
| 27 | v1.6.12 差异（CHANGELOG 未列） | `GetValidFileName` 增加 `maxBaseNameLength=100` 基名截断（保留扩展名），防超长标题触发 `PathTooLongException` | Go 签名只有 `(input, replacement, filterSlash)`，无长度截断：超长标题在 Windows 260 路径上限下可能直接失败 | ❌ | `internal/util/file.go:35`（签名无长度参数） | 增加 `maxBaseNameLength` 参数并在各调用点传默认 100 | P2 |
| 28 | v1.6.15 | 官方域名白名单收敛为唯一定义源（`HTTPUtil.OfficialHostSuffixes`/`IsOfficialBilibiliHost`，消除 `CookieTrustedHosts`/`UrlResolver.TrustedBilibiliHosts`/serve 三处漂移） | Go 只有一份后缀列表（`resolve.go` 内联），无漂移；但内容比上游少 `biliapi.com`，且未抽为共享常量（#7 的可信 host 校验落地时需复用同一份） | ⚠️ | `internal/workflow/resolve.go:292-299`（内联 8 项，缺 `biliapi.com`）；`internal/server/server.go` 无第二份主机白名单 | 抽 `util.OfficialHostSuffixes` + `IsOfficialHost`，补齐 `biliapi.com`，供 #7/#8 复用 | P2 |
| 29 | v1.6.15 | 解析层拦截负数分 P 参数（`-p -5` 不得静默降级为全量或崩溃） | Go `parsePageSelection` 对单值与区间都做 `n < 1` 拒绝并报可读错误 | ✅ | `internal/workflow/workflow.go:1324-1343`（`:1328`、`:1338`） | 无需改动 | P2 |
| 30 | v1.6.14 / v1.6.15 | 强制数字解析使用 `CultureInfo.InvariantCulture`（区域设置下解析异常） | Go `strconv` 无区域概念，该缺陷类别不存在 | ➖ | `internal/workflow/workflow.go:1324`、`internal/cli/commands.go:526` | 无需移植 | P2 |
| 31 | v1.6.16 / v1.6.18 | CLI 帮助文本修正：`--download-danmaku-formats`（仅支持 xml,ass）、`--danmaku-filter`（仅作用于 ASS）、`--comments`（仅第 1 个分P 下载一次）、`--skip-ai` 语义说明 | Go 帮助串仍是旧描述（`弹幕格式, 逗号分隔`/`弹幕关键词过滤`/`下载评论区`/`跳过AI字幕`），用户看不到枚举限制与作用域；`--show-all` 已提前对齐 | ⚠️ | `internal/cli/root.go:258-263`（对照 `:228` 已对齐） | 同步 4 处 flag usage 文案 | P2 |
| 32 | v1.6.19 / RF-67/68/69/76/77/87/88 | CI/测试门禁族（漏洞扫描门禁真实失败语义、tr-TR 假绿回归、AOT 绑定覆盖、local-integration 空跑、Docker smoke、测试名实相符） | C#/CI 基建特有：Go 侧无 NuGet 漏洞门禁、无 AOT 绑定测试、无对应 CI 作业，不构成行为差异 | ➖ | 不适用（Go CI 无对应作业） | 无需移植；若关注测试假绿，可在 Go 侧单独立项 | P2 |
| 33 | v1.6.14 | WBI 密钥材料长度校验（短于下限时降级保持原密钥，而非越界崩溃） | Go `GetMixinKey` 对 `len(orig) < 64` 返回空串由调用方降级，比上游 58 的下限更严，无越界路径 | ✅ | `internal/util/wbi.go:37-40` | 无需改动 | P2 |
| 34 | v1.6.12 差异（CHANGELOG 未列） | 登录会话初始化时估算 `SESSDATA` 剩余有效期并提前警告（serve 长驻进程跨月运行会静默失效）`EstimateSessdataExpiryDays` | Go 无等价实现（无 base64/JSON 解析 SESSDATA 的 expires 字段），长驻进程凭证过期只能在使用失败时暴露 | ❌ | grep `EstimateSessdataExpiry` / `即将过期` / `expires` 在 `internal/` 均 0 命中（仅 `internal/login/cookies.go` 做合并/提取） | 低优先；可在 `InitSession` 处解析 SESSDATA 剩余天数并在 <7 天时告警（fail-open） | P2 |

> 备注：#23/#24/#25/#27/#34 这 5 行来自 v1.6.11→v1.6.19 的 diff 走查（`Logger.cs`/`HTTPUtil.cs`/`PathUtil.cs`/`BBDownUtil.cs`），CHANGELOG 未单列条目，但属本片源码面且影响用户可见行为，故一并判定；来源列已明确标注。

## 归属其它范围的条目

- **A（serve/API）**：v1.6.12 `SanitizeUntrustedOptions` 净化 RetryCount/RetryDelay、清 Debug 标志、Host 剔除协议前缀；v1.6.15 `--serve-token` 与 `BBDOWN_SERVE_TOKEN` 优先级、`--trusted-proxy`；v1.6.14/17 认证限速、Origin/Content-Type 校验、`no-store`/`nosniff`/`Retry-After`；v1.6.17 serve 无 token 时的 Host 回环校验；RF-55 webhook 零地址、RF-56 `area` 白名单、RF-74 401 日志、RF-81 selectPage/danmakuFilter 上限、RF-82 废弃开关清零、RF-83/86、RF-71/84/85 文档族（Go `internal/server/server.go`）。
- **B（下载/混流/外部程序）**：v1.6.14 `FindExecutable` 不搜 CWD、RF-57 `ToolFinder` 不搜 CWD、v1.6.15 媒体下载切 `MediaDownloadClient`、ARIA2C 换行注入、RF-58/63/73 路径穿越、RF-43/44/72 两级过滤器、v1.6.17 mp4box 临时名、RF-45/64、v1.6.18 `--skip-mux` 无 ffmpeg 等（Go `internal/download`、`internal/muxer`）。
- **C（live/article/sub/watchlater/login）**：v1.6.12 扫码登录 `code == 0`/`access_token` 校验、Sub/WatchLater 捕获超时；v1.6.13 live 凭据与 `qn=30000`；v1.6.16 article/live `--work-dir`（Go 侧 `liveCmd`/`articleCmd` 只识别 `-o/--output`，不消费 `--work-dir`）；v1.6.17/18 sub check 取消语义、RF-30/32/59（登录流程侧）；RF-70 watchlater/live 标题脱敏；`ServerClock.cs`（本片 #6 的调用侧）。
- **E（fetcher/parser/DRM/评论/弹幕字幕）**：RF-52/53/65 先判 code 与诊断可达性、RF-80 `SanitizeServerText`（异常消息控制字符净化，其终端 sink 在 CLI 层）、RF-4 Widevine 许可证禁跳转、RF-60 选轨大小写、RF-79 的 DRM/登录读取点（已在 #12 汇总）、v1.6.15 异常消息剥离控制字符、v1.6.17 gRPC 硬编码 `Host` 头（Go `internal/appapi/appapi.go:151` 仍硬编码 `grpc.biliapi.net`）、v1.6.15 gRPC 必填参数 `allowEmpty` 语义（Go `internal/appapi/appapi.go:61-67,75-101` 对空 epId/qn 直接报错，与上游允许空值不同）。

## 未能判定（❓）汇总

- 本片无 ❓ 条目。两个原本静态阅读难以判定的判据已用本机 Go 1.27 探针实测后被判定为 #1（✅）与 #8/#9（⚠️）：
  1. `http.Client{Timeout}` 超时后 `errors.Is(err, context.Canceled)=false`、`Is(context.DeadlineExceeded)=true` → 内部超时不会被 CLI 取消分支吞掉。
  2. 携 Cookie 请求经重定向：**跨主机名**时标准库剥离 `Cookie`（目标侧收到空值），**同主机名不同端口**时保留；无 `Location` 的 3xx 原样返回；10 跳后报 `stopped after 10 redirects`。

## 建议的验证方式

- 静态回归（无需网络）：
  - `go test ./internal/config/... ` 后补一条 `--aria2c-proxy http://127.0.0.1:7890` + 配置含 URL 的用例，锁定 #19（当前 `internal/config/parser_test.go` 无该场景）。
  - 为 `internal/util/file.go` 的 `GetValidFileName` 补表驱动用例：`"video."`、`"video "`、`"..."`、101 字符长标题（锁定 #26/#27）。
- 集成/端到端（需本地假服务器，参照上游 `HttpUtilRetryTests.cs`/`CancellationClassificationTests.cs` 的行为规格）：
  - 假服务先返回 503 再返回 200：断言 Go 侧当前**不会**重试（#4 的失败证据），修复后断言最多 3 次且有退避。
  - 假服务对携 Cookie 的 GET 返回 302 → 另一主机：断言修复前 Cookie 被标准库剥离但请求仍发出、修复后应在跳转前被可信 host 校验拦下（#8）。
  - 假服务返回带 `Set-Cookie` 的 302：断言 `GetWebSourceWithSetCookies` 当前会采信跳转目标的 `Set-Cookie`（#8 的登录凭证注入面）。
  - 假服务返回 65MB 响应 / 200MB 解压的 gzip：断言当前进程内存被打满、修复后被拒（#12/#13）。
  - TV 登录两处 POST 指向假服务并返回 307：断言当前跟随、修复后报错（#11）。
  - `--host` 指向非 B 站主机（如本地假服务器）并观察是否携带 `Cookie`（#7）。
- 运行期观察：`BBDown <恶意构造的 URL 含 %0d%0a>` 观察日志是否被伪造行（#18）；Windows 上以 `video.` 为标题的视频验证落盘失败（#26）。
