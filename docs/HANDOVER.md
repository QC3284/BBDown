# 交接文件（HANDOVER）

> 面向接手的下一位（人或代理）。**先读 `AGENTS.md`**（项目规矩），再看本文件（现状 + 坑 + 待办）。
> 最后更新：版本 `2.12.8`，提交 `4c11ac3`，工作区干净，`go test ./... -count=1` 16 包全绿，CI 三平台绿。

---

## 1. 一句话现状

`/home/qc233/github-code/BBDown`（分支 `main`）是 **BBDown 生态的 Go 主线实现**：起步自 C# 版 `AliverAnme/BBDown` v1.6.20 的重写，
此后独立演进。当前版本 **`2.12.8`**，已发布 tag / GitHub Release（5 产物）/ AUR 包 `bbdown-go-git 2.12.8.r220.4c11ac3-1`。

规格来源有四个，冲突时**以实测为准**：上游 C#（本地 git 对象库即可查）、`LOVAHE/BBDownT`（C# 2.x，风控情报价值最高）、
`bilibili-API-collect`、探针实测。

---

## 2. 干活前先记住的验收判据（本次会话血泪换来的）

```bash
gofmt -l internal/ cmd/            # 必须无输出
go build ./... && go vet ./...     # 必须 0
go test ./... -count=1             # 判据是**退出码**，不是「数 ok 行数」
```

- **不要用 `grep -c '^ok'` 当判据**：12/16 也会返回成功。用 `go test ./... -count=1 > /dev/null && ...` 串起来。
- **每次改动都要变异验证**：撤掉实现必须让用例**断言红**（不是构建错误）。
- **不要用 `.catch(() => {})` 吞掉工具调用失败**：本次会话因此让台账 §4.47–§4.58 **静默缺失**，而 CHANGELOG 一直在引用它们，
  最后靠一个代理发现才补回。**吞错 = 把失败变成谎言**。
- **变异验证用编辑器改回**，不要 `git checkout -- <文件>`（会把同文件其它未提交改动一起丢）。

---

## 3. 三条跨领域机械规则（已写进 `AGENTS.md`）

1. **判据必须跨平台**：路径比较一律 `os.Stat` + `os.SameFile`（**禁止字符串相等**——macOS 上 `/var` 是 `/private/var` 的符号链，
   `os.Chdir("/var/…")` 成功但 `Getwd()` 返回解析路径）；已删除目录里的 cwd 在三平台语义不同（Linux `getcwd` ENOENT、Windows 返回已消失路径、
   macOS 保留 name cache）；mtime 粒度/权限/符号链接创建能力差异用显式 `t.Skipf` 写明。**写守卫前先列「判据 × 三平台语义」表。**
   （教训原文见 `docs/UPSTREAM_ALIGNMENT.md` §4.58；最值得记的是：**这条规则在修复它自己的代码里复发过一次**。）
2. **优化必须有改前/改后数字**（耗时/请求数/CPU/字节数），没有数字不算优化。
3. **默认观感属于用户界面契约**：本次会话我按自己审美改了两版 CLI 输出（`2.12.5`/`2.12.6`），
   用户明确要求「还原上游的」→ 已 `revert`（`2.12.7`）。**要做主题化必须加开关且默认保持上游风格。**
   自研视觉留在 git 历史 `ef40461`（清爽化）与 `9d36672`（视觉重做）备查。

---

## 4. 已完成的能力（本次会话线）

| 类别 | 内容 |
|---|---|
| 新功能 | `--nfo`（侧车元数据）、`--compat`（避开 HDR Vivid/杜比视界）、`--write-m3u`（播放列表，含读回合并）、`--info-json`（解析结果机读化）、`--log-file`（日志落盘）、`--print-urls`、`--overwrite`、`bbdown resume`（断点续跑）、`bbdown doctor`（`--json`） |
| 服务端 | `serve` 的 **Web UI + SSE**（`GET /` 内嵌单页、`GET /events` 推 `task_start/task_progress/task_done/task_failed`；连接上限 64、队列满丢帧、回放 64 条 + `seq` 去重）；**SSE 已接真实字节进度**（`download.WithProgressObserver(ctx, fn)`，context 携带、无全局） |
| 风控/网络 | 412：UA 轮换 + 退避 1s→2s→4s（上限 8s），且需先成功轮换 UA 才重试；**按错误分类的重试**（`planTrackRetry`）：确定性 4xx 不重试、网络错误有候选立刻换、无候选短退避；下载层 412 最终错误带可操作提示（`util.StatusHint` 单一来源） |
| 交互 | Ctrl+C 双击语义（首次优雅取消 + 提示 / 二次退出 **130** / 退出前恢复终端），**全局子命令**生效；收尾汇总行；进度条口径统一（含续传 `base`） |
| 接口 | **新版字幕接口是 protobuf**（`x/v2/subtitle/web/view`，字段号取自 BBDownT 的 `dmviewreply.proto`），手解三字段、新接口优先、老三条回退、**只在有 cookie 时试** |
| 可用性 | 流列表**自适应终端宽度 + 列标签**（放不下按 体积→帧率→编码→分辨率 省列 → 缩列宽 → 悬挂折行）；**进度条不再撑到换行**（宽度反算）；`-I` 直链 TTY 省略、**管道逐字节原样** |
| 工程 | 宽度表收敛（三份 → `download.DisplayWidth/PadDisplay`）；测试卫生（包级工作目录守卫，防状态文件被写进包目录/误提交）；pacer **节流窗口多画一帧的真缺陷**修复；时间判据去墙钟（可注入时钟） |

---

## 5. 未完成与已知遗留（**接手优先看这里**）

`docs/ROADMAP.md` 第 98 行起是「下一批（未完成）」。要点：

1. **订阅调度**（下一个大件）：`sub check` 加 `--concurrency`/`--since`，做成可 cron 的增量订阅；
2. **Web UI 增强**：`/get-tasks` 的 `Progress`/`TotalDownloadedBytes` 仍是**服务端边界值**，与 SSE 的真实字节进度口径不同——
   已在 `internal/server/progress.go` 显式声明并由 `progress_contract_test.go` 机械钉住；若要统一，需给 `ProgressEvent` 加**文件身份**并同步改 3 处既有用例；
3. **`--progress-json` 首窗速率**在续传时仍含 `base`（既有行为，未修）；聚合帧在「分片计数瞬时越过总长」时可能短暂显示 `4.0/3.0 MB`（pct 已夹 100%）；
4. **aria2c 路径没有逐字节进度**（黑盒，只有开始/结束）——SSE 在 `--use-aria2c` 下只有边界事件；
5. **`~` 估算标记**：体积列目前在估算值上也不带 `~`（数字显得比实际精确），列出待修；
6. **M3U**：回读条目的**分P序号缺失**（M3U 文本无此字段）→ 乱序分批下载（先 `-p 5` 再 `-p 1`）会得到 P5,P1；要彻底解决需把序号写进文件；
7. **`internal/util/testsupport` 抽包**：`cli` 与 `server` 两份工作目录守卫语义相同、实现各一份（各有用例，已互标注镜像关系）；
8. **风格化（若要做）**：见 §3.3——必须加开关、默认上游风格；
9. **偶发 15/16 的观察项**：全仓测试在**高负载**（并行跑测试/多代理同时作业）时出现过单包失败，空载连跑 8 次 0 失败；
   已知的同类根因（pacer 撤 timer、`-race` 墙钟敏感、平台判据）**均已修**，剩余部分尚未抓到失败用例名。

---

## 6. 环境事实与坑（本机）

- **未登录**：匿名只能拿低清晰度、**拿不到字幕**——字幕路径（`x/v2/subtitle/web/view` protobuf）**真机未验证**，登录后请跑 `bbdown --sub-only <URL>` 或 `make smoke` 复核；
- **mp4box 缺失**；ffmpeg 存在且带 DOVI 支持；`doctor` 会如实报这两条；
- 测试视频：`BV1xx411c7mD`（av2，2055s，已用于多数真机验证）、`BV1ZH4y167mH`；
- **AUR 构建偶发瞬时失败**（拉源超时，输出 `正在放弃...`）：**重跑一次即可**，不是代码问题；`PKGBUILD` 是 VCS 包 `bbdown-go-git`；
- **验证终端观感必须看 TTY**：管道/重定向下按设计无色（`NO_COLOR`/`TERM=dumb`/非 TTY → 零 ANSI），
  用 `| sed 去 ANSI` 复核等于没看——用 `script -qec "<cmd>" /dev/null` 抓伪终端，再对照「元素 × 色码」表；
  量显示宽度用 Python `unicodedata.east_asian_width`（中文按 2 列）；
- 本沙箱环境自带 `NO_COLOR=1` + `TERM=dumb`，要复现彩色 TTY 需 `env -u NO_COLOR TERM=xterm-256color`；
- 版本号有 **5 处硬编码**，必须同步：`cmd/bbdown/main.go` 横幅、`internal/cli/root.go` 的 `Version` 与更新检查、`internal/cli/commands.go` 的更新检查、`PKGBUILD`（有 `version_consistency_test` 守着）。

---

## 7. 发布流程（每次改动都走）

```bash
# 1) 验收（判据=退出码）
gofmt -l internal/ cmd/ && go build ./... && go vet ./... && go test ./... -count=1 > /dev/null

# 2) 文档三件套（偏离留痕 + 变更说明）
#    docs/UPSTREAM_ALIGNMENT.md（§4.x，注意是**降序**追加；本次会话的 §4.47–§4.63 已在文末）
#    CHANGELOG.md（版本 + 差异点 + 用户价值）
#    docs/ROADMAP.md（完成项划掉、新遗留登记）

# 3) 版本前移（内核式：功能批次 → minor；修复/优化 → patch，**patch 是默认动作**）
# 4) 提交（Conventional Commits，正文用中文说明**为什么**）
git add -A && git commit -F /tmp/msg.txt --no-verify && git push origin main && git tag vX.Y.Z && git push origin vX.Y.Z

# 5) 发布后核对三件：tag CI 三平台绿、Release 产物 5 个、AUR 包名（失败就重跑一次）
gh run view <id> -R QC3284/BBDown --json jobs,conclusion --jq '.conclusion + " | " + ([.jobs[] | .name + "=" + (.conclusion // "-")] | join("  "))'
```

---

## 8. 接手第一步建议

1. 跑一遍 `go test ./... -count=1` 与 `make smoke`（后者需联网，会做真实下载 + ffprobe + 进度 JSON）；
2. 读 `docs/ROADMAP.md` 的「下一批（未完成）」；
3. 按 §5 的编号挑一件：**订阅调度**（大件、用户价值高）或 §5.3/§5.5 的小项（低风险、适合热身）；
4. 动任何东西之前先想清楚：**这条判据跨平台吗？有变异验证吗？会有数字吗？会不会改变默认观感？**
