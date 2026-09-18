# 变更日志

本文件遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

**版本号语义**：`<上游版本>-go`。例如 `1.6.19-go` 表示「行为已对齐到上游 C# v1.6.19」，
版本号本身就是对齐进度声明，而不是独立的产品版本线。

上游（aliveranme/BBDown）的同名版本条目仍是行为的权威描述；本文件只记录 Go 重写侧
**相对上游的落地情况**。逐条对账基线与判定见 [docs/UPSTREAM_ALIGNMENT.md](docs/UPSTREAM_ALIGNMENT.md)。

## [1.6.19-go.2] - 2026-09-19

补丁版本：三处与上游可见行为不一致的修正——终端配色、登录二维码极性、布尔选项的上游写法。

### 修复

- **终端配色（对齐上游 .NET ConsoleColor）**：ConsoleColor 的 Red/Cyan/White 是**亮色**变体，
  本实现写成了 31/36/37 这组暗色 SGR（横幅前景只给了 37），错误行、提示行与横幅都比上游暗。
  现统一映射到 9x 亮色系（AnsiRed=91、AnsiCyan=96、AnsiWhite=97），DarkYellow=33、
  DarkGray=90、DarkBlue 背景=44 保持原值。
- **登录二维码极性**：上游 `ConsoleQRCode.GetGraphic` 把模块矩阵里为 true 的深色模块画成
  ConsoleColor.Black；本实现画反了（深色模块用白底 47、浅色模块用黑底 40），整张码是反色的
  ——深色终端上多数扫码器仍认，浅色主题终端会扫不出来。
- **布尔选项的上游写法**：上游（System.CommandLine）布尔选项 arity 为 0..1，上游 README 就按 `--multi-thread false`
  文档化「关闭多线程」；而 pflag 只认
  `--flag=false`，于是 `false` 被当成输入地址去请求 API，用户看到的是
  `获取视频信息失败 (code=-400): 请求错误`——既关不掉多线程，报错也指向错误的原因。
  现于参数归一化层折平长/短别名，大小写不敏感（对应上游 Boolean.Parse），
  `--` 之后不再折行，配置文件里的同一写法一并折行。

### 变更

- README 的 `--multi-thread` 一行补上 `--multi-thread false` 的关闭写法（与上游一致）。
- 修正本文件与 docs/ 下共 173 处被误写成「反斜杠+反引号」的行内代码标记（例如把
  `GetValidFileName` 写成 `\`GetValidFileName\``）——那些反斜杠会在 GitHub 上原样显示。

## [1.6.19-go.1] - 2026-09-19

补丁版本：修正 `1.6.19-go` 发布后发现的问题。**该版本的二进制来自一个 CI 失败的提交**，
故不移动已发布的 tag，而是以补丁版本重新出包。

### 修复

- **文件名净化契约**：`GetValidFileName("")` 曾返回占位符，而调用方用空串表示「无值」；
  尾随点/空格裁剪同步更新既有测试的期望（对齐上游 v1.6.18）。
- **Windows 路径语义**：`filepath.IsAbs("/abs/x")` 在 Windows 为 false，根相对路径被错误并入
  `--work-dir`；改用 `isRootedPath`（对齐上游 `Path.IsPathRooted`）。
- **直播产物完整性**：录制结果三态化（区分「未录到内容」与「合成失败但分段可恢复」）；
  合成改为写 `<path>.staging` 并在通过大小校验后改名，不再留下看似完成的截断产物；
  此前保留的旧会话目录**只提示不删**。
- **并发安全**：目标路径独占锁，锁覆盖「存在性检查 → 下载 → 混流」，避免并发任务互相覆盖产物。
- **`--aria2c-args` 引号感知切分**：`strings.Fields` 会把带空格的引号值拆成多个参数，
  未闭合引号还会吞掉整段配置。
- **`article`/`live` 支持 `--work-dir`**：此前忽略该选项，产物落进程目录；文件名接入保留名防护。
- **登录有效期**：`SESSDATA` 剩余 ≤7 天时告警、已过期时提示重新登录（解析 fail-open，不误报）。
- **日志写失败退避**：连续 5 次写失败后暂停文件日志 30 秒，避免磁盘满/只读时每行重试一次的失败 open。
- **UA 版本池**前移到 130-150（过旧的版本本身就是指纹）。

### 工程

- 新增 `.github/workflows/release.yml`：推送 `v*` tag 即自动跑测试、构建 5 平台并挂载产物。
- `AGENTS.md` 补充测试纪律：推送前必须 `go test ./... -count=1`（默认会命中测试缓存）、
  不要用固定 `time.Sleep` 等待异步副作用、断言失败前先释放资源。

## [1.6.19-go] - 2026-09-19

首个完成全面对账的版本：以 C# 上游 v1.6.19 为目标，逐条审计 187 项行为变更
（✅ 50 / ⚠️ 35 / ❌ 84 / ➖ 17 / ❓ 1），关闭全部 7 条 P0，并移植上游 15 个解析夹具作为回归网。

### 修复 —— 正确性与数据完整性

- **混流必败**：`-metadata:s:*` / `-disposition` / `-map_chapters` 曾紧跟各自的 `-i`，
  被 ffmpeg 当成下一个输入文件的选项。带封面+章节、≥2 字幕或任何背景音轨时混流以
  `exit 234`（"cannot be applied to input url"）失败（对齐上游 v1.6.16）。
- **mp4box 丢弃配音/背景轨**：杜比视界自动切换 mp4box 时这些轨道被静默丢弃，随后被清理删除；
  现进入 `-add` 链并以 `-udta` 命名（对齐上游 v1.6.17）。
- **直播录制删除已录内容**：`defer os.RemoveAll(segRoot)` 会连历次保留的分段一起删，
  而三处日志却称"已保留"；读中断还会先删分段再判断取消，Ctrl+C 丢整段。
- **直播录制挂死**：无读停滞看门狗，重连上限 3 次。现 60s 看门狗 + 不限次指数退避（3s→30s），
  本地写盘失败归为终结态不再重试。
- **直播分段尾裁剪**：网络中断留下的半截 FLV 标签会让 concat demuxer 中止整场合成。
- **合并产物大小校验**：ffmpeg 遇坏段会截断并仍以 0 退出，紧随其后的分段清理会静默丢掉整场录制。
- **入档粒度**：多P稿件的所有分P共享同一 aid，原先下完第一个分P就写入 `BBDown.archives`，
  下次运行余下分P被判定已下载而跳过（对齐上游 RF-75 `ArchiveTracker`）。
- **CoverOnly 仍下载完整视频**：代码注释误称"matching C#: no early return"，上游实际明确提前返回。
- **混流非事务化**：直写 `savePath` 时中断会留下截断成品，并被"已存在, 跳过下载"永久当成已完成。
- **免二压重发协议整体缺失**：现 qn=0 首请求 + qn=127 重取，只有带 `dash.video` 的文档才接管，
  重取被拒时沿用首轮已验证响应，用户取消向上传播。
- **VOD 读停滞**：媒体下载用无总超时的 client，网络黑洞下 `io.Copy` 永久阻塞。
- **文件名卫生**：尾随点/空格被 Windows 静默丢弃；基名无长度上限。

### 修复 —— 安全

- **serve 门禁整片缺失**：未配 token 时中间件根本不挂——不是"校验不严"而是"没有校验"。
  现无条件安装 Host 校验（DNS rebinding）与 Origin/Content-Type 校验（CORS 简单请求 CSRF）。
- **路径穿越**：`<aid>` / `<cid>` / `<dfn>` / `<res>` / `<fps>` / `<videoCodecs>` /
  `<audioCodecs>` 与字幕 `lan` 裸替换；新增 `SanitizePathSegment` 并接入 13 处。
- **携凭据请求可被 3xx 引走**：跨主机重定向一律拒绝（登录轮询 / gRPC POST / TV 登录）。
- **畸形响应 panic**：零字节与截断的 `device.wvd`、垃圾 protobuf（长度溢出）、DRM 许可证解析
  无边界检查都会崩进程，而全仓 `recover()` 0 命中。
- **解析死循环**：未知 wire type 时游标不推进，同一批畸形输入即可挂死。
- **日志注入**：服务器可控文本（标题 / UP 名 / 接口 message）可含 CRLF 伪造日志行或 ANSI 转义。
- **serve token**：明文比较改为常量时间；新增 `BBDOWN_SERVE_TOKEN` 环境变量优先级。
- **认证失败限速**：token 原本可被无限次尝试。
- **新增 `--trusted-proxy`**：`X-Forwarded-For` 仅在请求确实来自配置的代理时采信。
- **aria2c 输入注入**：URL 直接来自接口 `base_url`，可带换行注入 `out=` / `dir=` 选项。
- **`/get-tasks` 查询并发限流**；超大请求体返回 413 而非通用 400。
- **DRM 取钥链**：贯通 context（取钥窗口最长约 6 分钟完全不可取消）、许可证请求改禁跳转 +
  64MB 有界读取，调用点包 2 分钟超时。
- **任务持久化**：唯一临时名 + 互斥串行写盘；内存已完成列表按上限裁剪；错误信息脱敏本机绝对路径。
- **serve 关停**：原先只等待不取消，在途任务既不落 Cancelled 也不落盘，30s 后被进程退出截断。

### 修复 —— 行为一致性

- `--host` 在 UGC playurl 路径上失效（硬编码 `api.bilibili.com`）。
- 配置合并的 URL 启发式把选项值（`--aria2c-proxy http://…`、`--work-dir av123`）当成目标 URL，
  压掉 `BBDown.config` 里的真实地址并报"缺少参数"。
- 指定 ep 未命中时返回空 Index，workflow 回落 ALL —— 请求单集却静默下载整季。
- `sub check` 全部视频失败仍以退出码 0 结束。
- SubOnly 无条件把字幕改成 `.srt`（ASS/JSON 轨道扩展名与内容不符）。
- 评论导出不建父目录；直播录制前不加载凭据（永远游客画质）；登录轮询顶层 `code` 未校验；
  空 `access_token` 会被写盘。
- 直播画质从固定的游客档 qn=10000 提升到 qn=30000 并按账号权限回落。
- DOVI 探针无超时；补 `WaitDelay`（否则探针 fork 出的子进程持有管道时仍会阻塞）。

### 测试

- 移植上游 15 个 `Fixtures/parser/*.json` 夹具与 `httptest` 回放基座（含按 query 分流的多轮响应，
  用于免二压重发与 INTL 双次请求）。
- 用例规模：19 个测试文件（无 `httptest`）→ **126 例通过**；`internal/muxer`、
  `internal/parser`、`internal/drm` 三个包从零测试到有回归网。
- 每条修复都以「**撤掉修复必须让测试变红**」验证（变异验证），避免留下假绿的回归网。

### 文档

- 新增 [docs/UPSTREAM_ALIGNMENT.md](docs/UPSTREAM_ALIGNMENT.md)：参照源、项目谱系、可复现的对账步骤、
  判定总表、P0/P1 队列、逐轮修复记录与剩余队列。
- 新增 [docs/alignment/](docs/alignment/)：五片审计明细，逐条附 Go 侧 `file:line` 证据。
