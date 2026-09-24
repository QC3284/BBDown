# AGENTS.md

## 项目

BBDown —— 命令行哔哩哔哩下载器，**上游 C# 版 [aliveranme/BBDown](https://github.com/aliveranme/BBDown)
的 Go 语言重写**。

- `cmd/bbdown/` —— 入口
- `internal/cli/` —— cobra 命令、参数归一化、配置文件合并
- `internal/config/` —— 选项与 `BBDown.config` 解析
- `internal/workflow/` —— 编排：分P选择、下载、清理、入档
- `internal/parser/` + `internal/fetcher/` + `internal/appapi/` —— 四种解析模式与各页源
- `internal/download/` + `internal/muxer/` —— 多线程下载与混流
- `internal/server/` —— `serve` HTTP API
- `internal/live/` `internal/article/` `internal/substore/` `internal/login/` `internal/drm/`

## 最高约束：以上游为基线，持续做优化与新功能

上游是**基线**而不是天花板：同步上游（吸收其修复与行为）仍是常规工作，但本项目**允许并鼓励**在
基线之上做优化与新功能。动手前仍然先确认上游怎么做——目的从「保持一致」变成「知道差异在哪」：

- 上游源码就在本地 git 对象库里（remote `upstream`，tags `v1.6.11`~`v1.6.20`），**无需联网**：
  ```bash
  git show v1.6.20:BBDown.Core/Parser.cs
  git show v1.6.20:BBDown.Tests/DownloadPipelineTests.cs
  ```
- 上游的 `CHANGELOG.md` 与 `docs/REVIEW_FINDINGS.md`（RF-1..RF-88）是行为规格的第二来源。
- 上游的 `BBDown.Tests/`（含 `FakeBilibiliApiServer` 与 `Fixtures/`）是**回归网与规格来源**：
  新行为优先移植其断言当基线，再叠加我们自己的。
- **本仓库的注释也会过时**：曾有一条 `matching C#: no early return` 的注释与上游行为完全相反。
  有疑问就查上游真身，不要信注释。
- 版本号语义为 `<上游基线>-go[.N]`：`1.6.20-go` = 基于上游 v1.6.20 的首个发布，
  `1.6.20-go.3` = 其上的第 3 个发布（修复/优化/新功能都走这个序号，换基线时才重置）。

### 两条硬规矩：偏离留痕、改动有证据

1. **偏离留痕**：与上游不同的行为逐条登记到 `docs/UPSTREAM_ALIGNMENT.md` 的差异表（§4.x），并在
   CHANGELOG 对应版本里写清「差异点 + 用户价值」。**有意偏离**与**尚未对齐**必须分开记，否则下次
   对账会把有意差异当成待修的落后项。
2. **证据门槛**：
   - 功能与修复：回归用例 + 变异验证（撤掉实现必须变红），与既有测试纪律一致；
   - **优化：改前/改后的量化数字**（耗时 / HTTP 请求数 / CPU / 内存 / 产物大小）。没有数字不算优化。
3. 候选清单与优先级见 `docs/ROADMAP.md`。

## 命令

```bash
make build            # go build -ldflags="-s -w" -o bin/BBDown ./cmd/bbdown
make test             # go test ./...

# 提交前必须全过
gofmt -l internal/ cmd/     # 必须无输出
go build ./... && go vet ./... && go test ./...
```

## 测试纪律

- **每条修复都要有回归用例**，并以「撤掉修复必须让测试变红」验证（变异验证）。没有验证过的
  回归网等同于没有。
- **变异验证用编辑器改回，不要用 `git checkout -- <文件>` 撤销**：它会把同一个文件里其它尚未
  提交的改动一起丢掉（本仓库真发生过一次，颜色常量被整块回滚）。
- **推送前必须跑 `go test ./... -count=1`**：默认的 `go test ./...` 会用测试缓存，本地可能报绿而
  CI（`-count=1`）报红。改动 `internal/util` 这类被广泛依赖的契约时尤其要留意。
- **改动既有函数的契约时，先跑一遍受影响的包**：仓库里已有的测试钉住了旧行为，若那次改动是
  有意的（对齐上游），就同步更新旧测试并在用例里写明理由。
- 断言要检验**行为**，不要检验实现细节；不要写永远成立或只驱动副本的断言（上游把这类叫做"假绿"，
  并在 RF-58/RF-60/RF-68/RF-75/RF-88 多次修正）。
- 外部工具用**假可执行文件**（脚本 dump argv）而非真实调用；需要真实 ffmpeg 时用
  `t.Skip` 保护并说明依赖。
- 解析层改动优先接到 `internal/parser/testdata/` 的夹具回放基座上（`fixture_test.go`）。
- 计时相关的用例把超时做成变量（如 `readStallTimeout`、`downloadStallTimeout`）以便测试收窄。
- **不要用固定 `time.Sleep` 等待异步副作用**：慢速 CI runner（尤其 windows-latest）上会变成时序竞态。
  改为轮询可观测的状态（磁盘字节数、channel 信号）并设上限。
- **断言失败前先释放资源**：若被测协程还挂在网络上，`t.Fatal` 会走 defer（如 `srv.Close()`）等待挂起的 handler，
  失败看起来像超时。先用 `cancel()` 断开再断言。
- `internal/live` 的回环流式 harness 在 GitHub 的 windows-latest 上不可靠（刷出的字节收不到，读挂到 60s 看门狗），
  该用例按平台跳过；跨平台逻辑请另找不依赖 socket 时序的断言方式。

## 提交与分支

- Conventional Commits：`fix(scope): …` / `feat(scope): …` / `test(…)` / `docs: …`，
  正文用中文说明**为什么**（现象 + 后果），而不只是改了什么。
- 一个提交只做一件事；跨模块的批量修复按包拆分。

## 与上游同步

```bash
git fetch upstream --tags
git rev-list --left-right --count origin/master...upstream/master   # 0 ahead / N behind
git show <tag>:CHANGELOG.md          # 逐版本行为要点
git show <tag>:docs/REVIEW_FINDINGS.md
```

上游发版后：更新 `docs/UPSTREAM_ALIGNMENT.md` 的 §1 参照源与 §4 判定表，按新增条目补用例，
再把版本号前移（`CHANGELOG.md` + 四处硬编码：横幅 `cmd/bbdown/main.go`、`internal/cli/root.go` 的
`Version` 与更新检查、`internal/cli/commands.go` 的更新检查 + `PKGBUILD`）。

## serve 子命令的坑

- 其选项（`-l`/`--max-concurrent`/`--serve-token`）不从 `BBDown.config` 读取。
- 非回环监听且未配 token 会**故意拒绝启动**（`docker run` 直接退出是预期行为，不是 bug）。
- `/health` **按设计无需认证**；限速与门禁只覆盖 API 路径。
