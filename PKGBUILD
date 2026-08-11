# Maintainer: QC3284 <qc3284@github>
# Go rewrite of BBDown — builds from GitHub source
pkgname=bbdown
pkgver=1.6.10
pkgrel=1
pkgdesc="一款命令行式哔哩哔哩下载器. Bilibili Downloader. (Go rewrite)"
arch=("x86_64" "aarch64")
url="https://github.com/QC3284/BBDown"
license=('MIT')
depends=("ffmpeg")
makedepends=("git" "go")
provides=("bbdown")
conflicts=("bbdown-bin" "bbdown-git")
source=("git+https://github.com/QC3284/BBDown.git#branch=main")
sha256sums=('SKIP')

pkgver() {
    cd "$srcdir/BBDown"
	    printf "1.6.10.r%s.%s" "$(git rev-list --count HEAD)" "$(git rev-parse --short HEAD)"
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
