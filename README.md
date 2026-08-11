# BBDown

命令行式哔哩哔哩下载器。Bilibili Downloader.

> **本分支为 Go 语言重构版本，与原 C# 版功能一致。**
> 不会主动增加新功能，仅做行为对齐维护。

## 安装

### Arch Linux (AUR)

```bash
# 从 GitHub 源码构建
git clone https://github.com/QC3284/BBDown.git -b main
cd BBDown
go build -o BBDown ./cmd/bbdown/
sudo cp BBDown /usr/bin/
```

### 手动编译

```bash
git clone https://github.com/QC3284/BBDown.git -b main
cd BBDown
go build -ldflags="-s -w" -o BBDown ./cmd/bbdown/
```

## 使用

```bash
BBDown https://www.bilibili.com/video/BV1xx411c7mD
BBDown -i -e "avc,hevc" -q "1080P 高码率" BV1xx411c7mD
BBDown --audio-only BV1xx411c7mD
BBDown login
BBDown serve
BBDown live 12345
```

### 子命令

| 命令 | 说明 |
|---|---|
| `login` | 通过 APP 扫描二维码登录 WEB 账号 |
| `logintv` | 通过 APP 扫描二维码登录 TV 账号 |
| `serve` | 以 HTTP API 服务器模式运行 |
| `live` | 录制 B 站直播流 |
| `article` | 下载专栏文章为 Markdown |

### 主要选项

| 选项 | 说明 |
|---|---|
| `-t, --use-tv-api` | 使用 TV 端解析 |
| `-a, --use-app-api` | 使用 APP 端解析 |
| `-e, --encoding-priority` | 编码优先级，例 `"hevc,av1,avc"` |
| `-q, --dfn-priority` | 画质优先级，例 `"1080P 高码率,720P 高清"` |
| `-i, --interactive` | 交互式选择清晰度 |
| `-I, --only-show-info` | 仅解析不下载 |
| `-p, --select-page` | 选择分 P，如 `-p 1,3,5` 或 `-p LAST` |
| `-d, --download-danmaku` | 下载弹幕 |
| `--audio-only` | 仅下载音频 |
| `--video-only` | 仅下载视频 |
| `--sub-only` | 仅下载字幕 |
| `--cover-only` | 仅下载封面 |
| `--use-aria2c` | 调用 aria2c 下载 |
| `--multi-thread` | 多线程下载（默认开启） |
| `--debug` | 输出调试日志 |

完整选项列表请运行 `BBDown --help`。

## 功能

- 支持普通视频、番剧、课程、合集、收藏夹、UP 主全部投稿
- 最高支持 8K / HDR / 杜比视界 / 杜比全景声
- 多线程下载，支持 aria2c
- 自动合并音视频（需 ffmpeg 或 mp4box）
- 弹幕下载与过滤（XML / ASS）
- 字幕下载（多 API 回退）
- 二维码登录（WEB / TV）
- API 服务器模式
- 直播录制

## 与上游的关系

本仓库 `main` 分支为 Go 语言重构，**功能与 [nilaoda/BBDown](https://github.com/nilaoda/BBDown) C# 原版完全一致**。

`master` 分支保留 C# 原版代码。

## License

MIT
