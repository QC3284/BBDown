# Maintainer: QC3284 <qc3284@github>
# Go 重写版 BBDown — 从 GitHub 源码构建 (-git 包)
pkgname=bbdown-go-git
pkgver=1.6.11
pkgrel=1
pkgdesc="一款命令行式哔哩哔哩下载器. Bilibili Downloader. (Go 重写)"
arch=("x86_64" "aarch64")
url="https://github.com/QC3284/BBDown"
license=('MIT')
depends=("ffmpeg")
makedepends=("git" "go")
provides=("bbdown")
# 与所有其他 bbdown 版本冲突
conflicts=("bbdown" "bbdown-bin" "bbdown-git")
source=("git+https://github.com/QC3284/BBDown.git#branch=main")
sha256sums=('SKIP')

pkgver() {
    cd "$srcdir/BBDown"
    printf "1.6.11.r%s.%s" "$(git rev-list --count HEAD)" "$(git rev-parse --short HEAD)"
}

build() {
    cd "$srcdir/BBDown"
    go build -ldflags="-s -w" -o BBDown ./cmd/bbdown/
}

package() {
    mkdir -p "$pkgdir/usr/bin"
    cp "$srcdir/BBDown/BBDown" "$pkgdir/usr/bin/BBDown"
    chmod 755 "$pkgdir/usr/bin/BBDown"
}
