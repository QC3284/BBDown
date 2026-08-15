# Third-Party Notices

本文档列出本项目构建时**链接**的第三方库及其许可证。本项目自身代码以
[MIT](LICENSE) 协议发布，本文档不改变其适用范围。

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

