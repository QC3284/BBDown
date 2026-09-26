package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// --nfo：产物旁写同名 .nfo。判定与写入抽成 writeNFOSidecar，直接单测（不依赖真实下载/ffmpeg）。
//
// 变异验证：去掉 writeNFOSidecar 里的 os.WriteFile → 本用例变红。
func TestWriteNFOSidecar(t *testing.T) {
	dir := t.TempDir()
	product := filepath.Join(dir, "主标题.mp4")
	if err := os.WriteFile(product, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	page := entity.Page{Index: 2, Title: "P2 分P", OwnerName: "UP主", PubTime: 1698553200}

	wf := New(config.DefaultMyOption(), util.NewHTTPClient(func() bool { return false }, func() string { return "" }, nil))

	// 开关关闭：不写。
	wf.writeNFOSidecar(product, "主标题", page)
	if _, err := os.Stat(product + ".nfo"); !os.IsNotExist(err) {
		t.Errorf("--nfo 未开时不该写侧车文件（err=%v）", err)
	}

	// 开关打开：写，内容含标题与 uniqueid。
	wf.Cfg.WriteNFO = true
	wf.writeNFOSidecar(product, "主标题", page)
	body, err := os.ReadFile(product + ".nfo")
	if err != nil {
		t.Fatalf("应当写出 %s.nfo：%v", product, err)
	}
	for _, want := range []string{"<movie>", "<title>P2 分P</title>", "<episode>2</episode>", "<studio>UP主</studio>"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("NFO 缺少 %q：%s", want, string(body))
		}
	}

	// 产物不存在：不写（没有可描述的对象），且不能报错。
	missing := filepath.Join(dir, "不存在.mp4")
	wf.writeNFOSidecar(missing, "主标题", page)
	if _, err := os.Stat(missing + ".nfo"); !os.IsNotExist(err) {
		t.Errorf("产物不存在时不该写侧车文件（err=%v）", err)
	}
}
