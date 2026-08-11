# BBDown

命令行式哔哩哔哩下载器。Bilibili Downloader.

> **本分支为 Go 语言重构版本，与原 C# 版功能一致。**
> 不会主动增加新功能，仅做行为对齐维护。

## 安装

### Arch Linux

```bash
git clone https://github.com/QC3284/BBDown.git -b main
cd BBDown
makepkg -si
```

### 手动编译

依赖：Go 1.21+、ffmpeg

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
```

### 子命令

| 命令 | 说明 |
|---|---|
| `login` | APP 扫描二维码登录 WEB 账号 |
| `logintv` | APP 扫描二维码登录 TV 账号 |
| `serve` | HTTP API 服务器模式 |
| `live` | 录制直播流 |
| `article` | 下载专栏文章为 Markdown |

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
| `--debug` | 输出调试日志 |
| `-c, --cookie` | 设置 Cookie |
| `--access-token` | 设置 Access Token |

完整选项见 `BBDown --help`。

## 功能

- 普通视频、番剧、课程、合集、收藏夹、UP 主全部投稿
- 最高 8K / HDR / 杜比视界 / 杜比全景声
- 多线程下载 + aria2c
- 自动合并音视频（需 ffmpeg 或 mp4box）
- 弹幕下载与过滤（XML / ASS）
- 字幕下载（多 API 回退）
- 二维码登录（WEB / TV）
- API 服务器模式
- 直播录制（断流自动重连）
- Widevine DRM 解密

## 与上游的关系

基于 [aliveranme/BBDown](https://github.com/aliveranme/BBDown) Go 语言重构，行为完全对齐。

| 分支 | 内容 |
|---|---|
| `main` | Go 重构（当前分支） |
| `master` | C# 原版 |

## 注意事项及警告

- 本软件仅供学习交流，**请勿用于商业用途或传播下载内容**。
- 使用本软件下载视频时，请遵守哔哩哔哩 [用户协议](https://www.bilibili.com/protocal/licence.html) 及相关法律法规。
- 下载受版权保护的内容可能构成侵权，请仅下载您拥有合法权限的内容。
- **本分支不会主动增加新功能**，仅保持与原 C# 版行为一致。
- 使用 `--cookie` 或 `--access-token` 时，凭据将以明文存储于本地文件，请注意保管。
- **禁止将本软件用于任何违法用途**，使用者自行承担一切法律后果。

## License

MIT © 2020 [nilaoda](https://github.com/nilaoda), 2025 [AliverAnme](https://github.com/aliveranme)

Go 重构同样以 MIT 协议发布。
