# Maintainer: QC3284 <qc3284@github>
pkgname=bbdown-go-git
pkgver=1.6.19.go.12
pkgrel=1
pkgdesc="一款命令行式哔哩哔哩下载器. Bilibili Downloader. (Go 重写)"
arch=("x86_64" "aarch64")
url="https://github.com/QC3284/BBDown"
license=('MIT')
depends=("ffmpeg")
makedepends=("git" "go")
options=(!debug)
provides=("bbdown")
conflicts=("bbdown" "bbdown-bin" "bbdown-git" "bbdown-debug" "bbdown-bin-debug" "bbdown-git-debug")
source=("git+https://github.com/QC3284/BBDown.git#branch=main")
sha256sums=('SKIP')

pkgver() {
    cd "$srcdir/BBDown"
    # Track the release tag (v1.6.19-go.12 -> 1.6.19.go.12; "-" is not allowed in
    # an Arch version) and append the commit distance so VCS builds stay ordered.
    tag=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//; s/-/./g')
    [ -n "$tag" ] || tag="1.6.19"
    printf "%s.r%s.%s" "$tag" "$(git rev-list --count HEAD)" "$(git rev-parse --short HEAD)"
}

build() {
    cd "$srcdir/BBDown"
    go build -trimpath -ldflags="-s -w" -o BBDown ./cmd/bbdown/
}

package() {
    mkdir -p "$pkgdir/usr/bin"
    cp "$srcdir/BBDown/BBDown" "$pkgdir/usr/bin/BBDown"
    chmod 755 "$pkgdir/usr/bin/BBDown"
}
