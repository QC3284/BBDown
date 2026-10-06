# Third-Party Notices

本文档列出本项目构建时**链接**的第三方库及其许可证。本项目自身代码以
[MIT](LICENSE) 协议发布，本文档不改变其适用范围。

除链接依赖外，发布包还附带**运行时组件**（DRM 解密用的 mp4decrypt 与 device.wvd）——
它们不参与 Go 构建链接，许可与来源见文末「随发布包分发的运行时组件」。

各许可证的完整原文以**逐字（verbatim）**方式存放于 [LICENSES/](LICENSES/) 目录，
每个依赖一个独立文件，便于整体复制；文件名形如 `<依赖>-<SPDX 标识>.txt`。

本仓库不内置第三方源码，构建时由 Go 模块系统按 `go.mod` / `go.sum` 获取下列依赖：

| 依赖 | 版本 | SPDX 标识 | 许可证原文 |
|---|---|---|---|
| github.com/spf13/cobra | v1.10.2 | Apache-2.0 | [LICENSES/cobra-Apache-2.0.txt](LICENSES/cobra-Apache-2.0.txt) |
| github.com/spf13/pflag | v1.0.10 | BSD-3-Clause | [LICENSES/pflag-BSD-3-Clause.txt](LICENSES/pflag-BSD-3-Clause.txt) |
| github.com/skip2/go-qrcode | v0.0.0-20200617195104-da1b6568686e | MIT | [LICENSES/go-qrcode-MIT.txt](LICENSES/go-qrcode-MIT.txt) |
| golang.org/x/term | v0.15.0 | BSD-3-Clause | [LICENSES/x-term-BSD-3-Clause.txt](LICENSES/x-term-BSD-3-Clause.txt) |
| golang.org/x/sys | v0.29.0 | BSD-3-Clause | [LICENSES/x-sys-BSD-3-Clause.txt](LICENSES/x-sys-BSD-3-Clause.txt) |
| github.com/inconshreveable/mousetrap | v1.1.0 | Apache-2.0 | [LICENSES/mousetrap-Apache-2.0.txt](LICENSES/mousetrap-Apache-2.0.txt) |

## 再分发要求说明

- **BSD-3-Clause**（pflag、x/term、x/sys）：其条款要求以二进制形式再分发时，
在随分发提供的文档或其他材料中复现版权声明、条件列表与免责声明。
本仓库以 `LICENSES/` 下的逐字原文满足该项要求。
- **Apache-2.0**（cobra、mousetrap）：要求随分发提供许可证副本，
并在上游随附 `NOTICE` 文件时复现其内容；上述两个依赖均未随附 `NOTICE` 文件，
故仅需提供许可证副本（已满足）。
- **MIT**（go-qrcode）：要求在所有副本或实质性部分中保留版权声明与许可声明
（已满足）。

## Go 标准库

本程序以 Go 语言编译，链接 Go 标准库（BSD 风格许可证，文本见
<https://go.dev/LICENSE>）。本仓库未转载该文本；再分发要求请以
<https://go.dev/LICENSE> 与 <https://go.dev/doc/faq> 的官方说明为准。

## 版本变动

升级依赖版本（`go get` / `go mod tidy`）后，请同步更新上表版本号，
并以新版依赖自带的 LICENSE 文件覆盖 `LICENSES/` 下对应文件。

## 随发布包分发的运行时组件

下列组件由 .github/workflows/release.yml 打进发布包（与二进制**同目录**，运行时按
「显式路径 → 程序目录 → PATH」解析，见 internal/drm/detect.go）。它们**不参与 Go 构建链接**，
不被本项目静态或动态链接，本项目与它们是聚合分发关系，各自适用自己的许可：

| 组件 | 来源 | 许可证 | 说明 |
|---|---|---|---|
| mp4decrypt（Bento4） | axiomatic-systems/Bento4 的 SDK 归档；归档地址与 SHA256 由仓库变量 BENTO4_URL_&lt;PLATFORM&gt; / BENTO4_SHA256_&lt;PLATFORM&gt; 固定，流水线逐个校验后才装入 | GPL-2.0（逐字原文见 [LICENSES/Bento4-GPL-2.0.txt](LICENSES/Bento4-GPL-2.0.txt)；上游另提供商业授权） | DRM 内容解密的外部工具。**linux-arm64 官方没有预编译包**：该平台归档不含它，运行时按 internal/drm.Mp4decryptInstallHint 的指引改用发行版仓库或 --mp4decrypt-path |
| device.wvd | **不进入源码树**：发布时仅在显式配置仓库 secret BBDOWN_WVD_BASE64 时注入，并以仓库变量 BBDOWN_WVD_SHA256 固定校验；未配置时发布包不含该文件 | 专有（Widevine L3 设备文件，无开源许可） | 仅当你自己拥有并有权使用该设备文件时才应打包或使用。分发许可属于灰色地带：本项目不提供该文件，源码树中也不包含它；使用者需自行获取并对合规负责。没有它时仍可用 --key/--kid 手动解密 |

再分发要求：

- **GPL-2.0（mp4decrypt）**：以独立可执行文件形式随归档分发，未与本项目代码链接；
  归档内同时提供许可证原文（LICENSES/Bento4-GPL-2.0.txt，FSF 的 GPL-2.0 逐字文本）。
  若不接受该许可条款，可自行从发行包中删除该文件，或改用 --mp4decrypt-path
  指向系统安装的 Bento4（例如发行版仓库里的 bento4 包）。
- **device.wvd（专有）**：本项目在源码与默认构建中都不包含它；本地构建（make build）
  自然形成「没有内置 wvd」的形态，运行时按 internal/drm 的前置检查给出可操作指引
  （--wvd-path 指定自备文件，或 --key/--kid 手动解密）。
