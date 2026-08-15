# BBDown

命令行式哔哩哔哩下载器。Bilibili Downloader.

> **本分支为 Go 语言重写版本，与上游 [aliveranme/BBDown](https://github.com/aliveranme/BBDown)（C# 版 v1.6.11）功能一致。**
> 不主动增加新功能，仅做行为对齐维护；对齐以**效果一致**为准，
> 上游明显不合理的小问题会以等价方式修正（详见下文"与上游的关系"）。
> Go 重写由 AI 辅助完成。

## 安装

### Arch Linux

```bash
git clone https://github.com/QC3284/BBDown.git -b main
cd BBDown
makepkg -si
```

> 如果你能将此项目上传至 [Arch Linux AUR](https://aur.archlinux.org/)，我将非常感谢你。

### 手动编译

依赖：Go 1.26+、ffmpeg（直播分段合成/混流需要 ffmpeg；也可用 mp4box 混流）

```bash
git clone https://github.com/QC3284/BBDown.git -b main
cd BBDown
go build -ldflags="-s -w" -o BBDown ./cmd/bbdown/
sudo cp BBDown /usr/bin/
```

## 使用

```bash
# 基本下载
BBDown https://www.bilibili.com/video/BV1xx411c7mD

# 指定编码和画质优先
BBDown -e "avc,hevc" -q "1080P 高码率" BV1xx411c7mD

# 仅下载音频
BBDown --audio-only BV1xx411c7mD

# 交互式选择
BBDown -i BV1xx411c7mD

# 仅查看信息
BBDown -I BV1xx411c7mD

# 登录
BBDown login

# API 服务器模式
BBDown serve

# 直播录制
BBDown live 12345

# 下载专栏为 Markdown
BBDown article cv123

# 下载稍后再看列表（需登录）
BBDown watchlater

# 订阅管理（add/list/remove/check）
BBDown sub add mid:123456
BBDown sub check
```

### 子命令

| 命令 | 说明 |
|---|---|
| `login` | APP 扫描二维码登录 WEB 账号 |
| `logintv` | APP 扫描二维码登录 TV 账号 |
| `serve` | HTTP API 服务器模式（非回环监听必须配 `--serve-token`） |
| `live` | 录制直播流（断流自动重连，分段合成，支持 `-o`） |
| `article` | 下载专栏文章为 Markdown（支持 `-o`） |
| `watchlater` | 下载稍后再看列表（`--limit`，需登录） |
| `sub` | 订阅管理：`add/list/remove/check` |

### 主要选项

| 选项 | 说明 |
|---|---|
| `-t, --use-tv-api` | TV 端解析 |
| `-a, --use-app-api` | APP 端解析 |
| `--use-intl-api` | 国际版解析 |
| `-e, --encoding-priority` | 编码优先级，逗号分隔 |
| `-q, --dfn-priority` | 画质优先级，逗号分隔 |
| `-i, --interactive` | 交互式选择清晰度 |
| `-I, --only-show-info` | 仅解析信息，不下载 |
| `-p, --select-page` | 选择分 P：`-p 1,3,5`、`-p 3-5`、`-p LAST` |
| `-d, --download-danmaku` | 下载弹幕 |
| `--danmaku-filter` | 弹幕关键词过滤 |
| `-F, --file-pattern` | 单 P 文件名模板 |
| `-M, --multi-file-pattern` | 多 P 文件名模板 |
| `--audio-only` | 仅下载音频 |
| `--video-only` | 仅下载视频 |
| `--sub-only` | 仅下载字幕 |
| `--cover-only` | 仅下载封面 |
| `--danmaku-only` | 仅下载弹幕 |
| `--skip-mux` | 跳过混流 |
| `--use-aria2c` | 调用 aria2c 下载 |
| `--multi-thread` | 多线程下载（默认开启） |
| `--force-http` | 强制 HTTP 协议（默认关闭，mcdn 域名除外） |
| `--comments` | 下载评论区（导出 .comments.json） |
| `--thread-segment-size` | 多线程分片大小(MB，默认 20) |
| `--save-archives-to-file` | 记录已下载 aid（`BBDown.archives`，`aid|` 格式） |
| `--retry-count` / `--retry-delay` | 下载请求重试次数/间隔（默认 3 次 / 3000ms） |
| `--config-file` | 指定配置文件（逐行参数文本，默认 `BBDown.config`） |
| `--debug` | 输出调试日志 |
| `-c, --cookie` | 设置 Cookie |
| `--access-token` | 设置 Access Token |

完整选项见 `BBDown --help`。

### 配置文件

默认位置为可执行文件同目录下的 `BBDown.config`（可用 `--config-file` 指定），
逐行参数文本，与命令行参数同语法：

```text
# 注释以 # 开头
--encoding-priority
hevc,avc
--skip-cover
https://www.bilibili.com/video/BV1xx411c7mD
```

- 命令行显式给出的选项优先于配置文件；配置中的 URL 仅在命令行未提供目标时生效。
- 子命令（login/serve/live/article/watchlater/sub）不读取配置文件。

### API 服务器

`BBDown serve` 启动 HTTP API（默认 `http://127.0.0.1:23333`）；监听非回环地址
（`0.0.0.0`/`::`/网卡 IP）时必须配置 `--serve-token`，此时所有 API 需携带
`X-Serve-Token` 请求头。

| 端点 | 方法 | 说明 |
|---|---|---|
| `/get-tasks` | GET | 全部任务（`Running` / `Finished`） |
| `/get-tasks/running` | GET | 进行中任务 |
| `/get-tasks/finished` | GET | 已完成任务 |
| `/get-tasks/{id}` | GET | 按 JobId / Aid / URL 查询单个任务 |
| `/add-task` | POST | 提交下载任务，请求体 `{"Url": "..."}`，返回 202 + `{"TaskId": "..."}` |
| `/cancel/{id}` | POST | 取消排队中/进行中的任务 |
| `/remove-finished` | DELETE | 清空已完成任务 |
| `/remove-finished/failed` | DELETE | 只清除失败任务 |
| `/remove-finished/{id}` | DELETE | 清除单个已完成任务 |
| `/health` | GET | 健康检查（无需 token） |

任务 JSON 为 PascalCase 契约（`JobId/Aid/Url/Status/Progress/SavePaths/...`），
完成列表持久化到 `bbdown-tasks.json`（保留 30 天 / 最近 1000 条）。回调
`--notify-webhook` 仅接受公网地址（内网/回环/云元数据地址会被拒绝）。

### 测试与 CI

```bash
make test        # go test ./...
```

推送与 PR 会触发 GitHub Actions（build/vet/test/gofmt，覆盖 linux/windows/macos）。

## 功能

- 普通视频、番剧、课程、合集、收藏夹、UP 主全部投稿（合集/系列/收藏夹完整分页）
- 最高 8K / HDR / 杜比视界 / 杜比全景声（杜比视界在 ffmpeg<5.0 时自动切换 mp4box 混流）
- 三种解析模式：WEB（WBI 签名）/ APP（gRPC protobuf）/ TV / 国际版
- 多线程下载（并发上限 8、Range 校验、分片重试）+ aria2c
- 断点续传（`.tmp` + 资源身份清单，ETag/Last-Modified 校验，中断可安全续传）
- 低画质 FLV 流分段下载与合并
- 自动合并音视频（ffmpeg / mp4box，含章节、封面、多音轨、`creation_time` 元数据）
- 弹幕下载与过滤（XML / ASS）、字幕下载（多 API 回退、145 项语言表）
- 章节信息写入（`view_points`）
- 二维码登录（WEB / TV，含 qrcode.png）、凭据脱敏日志
- API 服务器模式（任务持久化、认证、限流、SSRF 回调防护）
- 直播录制（分段录制 + 断流自动重连 + 指数退避 + ffmpeg 合成）
- 专栏下载为 Markdown、评论区导出（.comments.json）、稍后再看、订阅管理
- 逐行参数配置文件（`BBDown.config`）
- Widevine DRM 解密（取钥 + mp4decrypt 执行 + 失败传播）
- 启动时检查新版本（GitHub Releases）

## 与上游的关系

基于 [aliveranme/BBDown](https://github.com/aliveranme/BBDown)（C# 版 v1.6.11）Go 语言重写。
CLI 选项、默认值、API 端点、解析/下载/混流行为均已逐项对齐；对齐以**效果一致**为准，
以下上游明显不合理之处以等价方式修正：

- `-p -5` / `-p 0` 等非法分P直接报错（上游将其当作 token 拖延到"所选分P不存在"才失败）。
- 番剧/国际版分P保留 API 返回的真实时长（上游硬编码 0，依赖 playurl 兜底）。
- 更新检查失败仅输出 debug 日志（上游打 warning 打扰普通用户）。
- 断点续传身份校验更保守（URL+ETag/Last-Modified 任一不符即重下，宁可不续传也不拼出损坏文件）。

| 分支 | 内容 |
|---|---|
| `main` | Go 重写（当前分支） |
| `master` | C# 原版快照 |

## 注意事项及警告

- 本软件仅供学习交流，**请勿用于商业用途或传播下载内容**。
- 使用本软件下载视频时，请遵守哔哩哔哩 [用户协议](https://www.bilibili.com/protocal/licence.html) 及相关法律法规。
- 下载受版权保护的内容可能构成侵权，请仅下载您拥有合法权限的内容。
- **本分支不会主动增加新功能**，仅保持与上游 C# 版功能一致（效果一致，见"与上游的关系"）。
- 使用 `--cookie` 或 `--access-token` 时，凭据将以明文存储于本地文件，请注意保管。
- **禁止将本软件用于任何违法用途**，使用者自行承担一切法律后果。

## License

MIT © 2020 [nilaoda](https://github.com/nilaoda), 2025 [AliverAnme](https://github.com/aliveranme), 2026 [QC3284](https://github.com/QC3284)

Go 重写同样以 MIT 协议发布。
