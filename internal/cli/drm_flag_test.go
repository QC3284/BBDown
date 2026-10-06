package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QC3284/BBDown-go/internal/config"
	"github.com/QC3284/BBDown-go/internal/drm"
)

// ---- t56：DRM 开关接线（--decrypt-drm 默认开 / --no-decrypt-drm 关闭）+ doctor 三态 ----

// TestDecryptDrmFlagMatrix 是 flag 默认值矩阵：默认开启、负向开关优先、旧脚本的
// 显式 --decrypt-drm=false 仍然有效——而且三条路径都要**真的进到 cfg**（不是只改 flag 文本）。
//
// 变异验证：把 --decrypt-drm 的默认值改回 false → 本用例红（默认那一格）。
func TestDecryptDrmFlagMatrix(t *testing.T) {
	flag := rootCmd.Flags().Lookup("decrypt-drm")
	if flag == nil {
		t.Fatal("缺少 --decrypt-drm 参数")
	}
	if flag.DefValue != "true" {
		t.Errorf("--decrypt-drm 默认值应当是 true（DRM 自动解密默认开启，吸收上游 1.7.1），实际 %q", flag.DefValue)
	}
	noFlag := rootCmd.Flags().Lookup("no-decrypt-drm")
	if noFlag == nil {
		t.Fatal("缺少 --no-decrypt-drm 参数（关闭自动检测/解密的显式开关）")
	}
	if noFlag.DefValue != "false" {
		t.Errorf("--no-decrypt-drm 默认值应当是 false，实际 %q", noFlag.DefValue)
	}

	cases := []struct {
		name                  string
		decryptDrm, noDecrypt bool
		want                  bool
	}{
		{"默认（都不给）→ 自动解密开", true, false, true},
		{"--no-decrypt-drm → 关闭", true, true, false},
		{"旧脚本显式 --decrypt-drm=false → 关闭", false, false, false},
		{"两个都给 → 关闭（负向优先）", false, true, false},
	}
	origDec, origNo := optDecryptDrm, optNoDecryptDrm
	defer func() { optDecryptDrm, optNoDecryptDrm = origDec, origNo }()
	for _, c := range cases {
		if got := decryptDrmEnabled(c.decryptDrm, c.noDecrypt); got != c.want {
			t.Errorf("%s：decryptDrmEnabled(%v, %v) = %v, want %v", c.name, c.decryptDrm, c.noDecrypt, got, c.want)
		}
		// 端到端：真的写进了 cfg（buildMyOption 是所有命令的选项来源）。
		optDecryptDrm, optNoDecryptDrm = c.decryptDrm, c.noDecrypt
		if got := buildMyOption().DecryptDrm; got != c.want {
			t.Errorf("%s：buildMyOption().DecryptDrm = %v, want %v（开关没接进 cfg）", c.name, got, c.want)
		}
	}
}

// TestDoctorDrmAssetsThreeStates 逐词钉住 doctor 的 DRM 组件三态（与下载前前置检查同一文案）：
//   - 齐备 → ok（详情列出两个件与来源）；
//   - 缺 device.wvd 且无手动密钥 → warn（不是 fail：不下 DRM 内容就不需要），详情给 --wvd-path/--key 指引；
//   - 手动 --key/--kid 齐备 → ok（不需要 device.wvd）。
//
// 变异验证：撤掉 doctorChecks 里的 checkDrmAssets / 撤掉前置检查调用 → 本用例红。
func TestDoctorDrmAssetsThreeStates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mp4 := filepath.Join(dir, "mp4decrypt")
	wvd := filepath.Join(dir, drm.BundledFileName)
	if err := os.WriteFile(mp4, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wvd, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 态一：齐备（显式路径；显式优先于内置与 PATH）。
	res := checkDrmAssets(ctx, config.MyOption{Mp4decryptPath: mp4, WvdPath: wvd}, nil)
	if res.Name != "DRM 解密" || res.Level != "ok" {
		t.Fatalf("齐备时应当 ok：%+v", res)
	}
	for _, want := range []string{
		"mp4decrypt=" + mp4 + "(explicit)",
		drm.BundledFileName + "=" + wvd + "(explicit)",
	} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("详情缺少 %q：%q", want, res.Detail)
		}
	}
	if strings.Contains(res.Detail, "手动 --key") {
		t.Errorf("没有手动密钥时不该提手动密钥：%q", res.Detail)
	}

	// 态二：有 mp4decrypt、缺 device.wvd、无手动密钥 → warn + 可操作指引。
	res = checkDrmAssets(ctx, config.MyOption{Mp4decryptPath: mp4}, nil)
	if res.Level != "warn" {
		t.Fatalf("缺 device.wvd 应当是 warn（DRM 内容会拒绝下载；普通内容不受影响）：%+v", res)
	}
	for _, want := range []string{drm.BundledFileName, "--wvd-path", "--key", "--kid"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("缺件指引缺少 %q：%q", want, res.Detail)
		}
	}

	// 态三：手动 --key/--kid 齐备 → ok（不需要 device.wvd）。
	res = checkDrmAssets(ctx, config.MyOption{
		Mp4decryptPath: mp4,
		DrmKeyHex:      strings.Repeat("ab", 16),
		DrmKidHex:      strings.Repeat("cd", 16),
	}, nil)
	if res.Level != "ok" {
		t.Fatalf("手动密钥齐备时应当 ok：%+v", res)
	}
	if !strings.Contains(res.Detail, "手动 --key/--kid") {
		t.Errorf("应当说明「手动密钥齐备、不需要 device.wvd」：%q", res.Detail)
	}

	// 态四：什么都没给（隔离 PATH，避免本机装了 Bento4 让断言变成环境相关）→ warn，
	// 且 device.wvd 一定缺失（它不会进 PATH）。
	t.Setenv("PATH", t.TempDir())
	res = checkDrmAssets(ctx, config.MyOption{}, nil)
	if res.Level != "warn" {
		t.Fatalf("全缺时应当 warn：%+v", res)
	}
	for _, want := range []string{"mp4decrypt", drm.BundledFileName + "=缺失"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("全缺时的详情缺少 %q：%q", want, res.Detail)
		}
	}
}

// TestDoctorRegistersDrmAssetsCheck 钉住接线：默认自检项里必须有 DRM 组件那一项
// （函数比不了，用函数指针比）。
func TestDoctorRegistersDrmAssetsCheck(t *testing.T) {
	want := reflect.ValueOf(checkDrmAssets).Pointer()
	for _, check := range doctorChecks {
		if reflect.ValueOf(check).Pointer() == want {
			return
		}
	}
	t.Fatalf("doctorChecks 里没有注册 checkDrmAssets（共 %d 项）", len(doctorChecks))
}

// TestDoctorRendersDrmRow 逐词钉住渲染：ok 用 +、warn 用 !，行里带「DRM 解密」与详情。
func TestDoctorRendersDrmRow(t *testing.T) {
	dir := t.TempDir()
	mp4 := filepath.Join(dir, "mp4decrypt")
	if err := os.WriteFile(mp4, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	okRows := renderDoctorRows([]doctorResult{checkDrmAssets(ctx, config.MyOption{Mp4decryptPath: mp4}, nil)})
	if len(okRows) == 0 || len(okRows[0].Lines) == 0 {
		t.Fatalf("渲染结果为空：%+v", okRows)
	}
	if okRows[0].Level != "warn" || !strings.HasPrefix(okRows[0].Lines[0], "!") {
		t.Errorf("缺件的行应当是 ! 开头（warn）：%q", okRows[0].Lines[0])
	}
	if !strings.Contains(okRows[0].Lines[0], "DRM 解密") {
		t.Errorf("行首应当有自检项名：%q", okRows[0].Lines[0])
	}

	wvd := filepath.Join(dir, drm.BundledFileName)
	if err := os.WriteFile(wvd, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	okRows = renderDoctorRows([]doctorResult{checkDrmAssets(ctx, config.MyOption{Mp4decryptPath: mp4, WvdPath: wvd}, nil)})
	if okRows[0].Level != "ok" || !strings.HasPrefix(okRows[0].Lines[0], "+") {
		t.Errorf("齐备的行应当是 + 开头（ok）：%q", okRows[0].Lines[0])
	}
}
