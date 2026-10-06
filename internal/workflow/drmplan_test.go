package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/drm"
	"github.com/QC3284/BBDown/internal/entity"
)

// ---- t49：DRM 自动解密判定矩阵 + 下载前前置检查 ----
//
// 判定被抽成纯函数 planDrmDecryption（配置 + 解析结果 + 程序目录 → 要不要解密、外部件状态），
// 所以「默认开启 / --no-decrypt-drm 关回旧形态 / 缺件在下载前失败 / 手动密钥不需要 wvd」
// 这些格子在离线用例里逐格断言，不必真跑一次加密下载。

func writeAsset(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestPlanDrmDecryptionMatrix 是开关矩阵（t49 ①）：默认（Cfg.DecryptDrm=true）时
// DRM 内容自动解密并做前置检查；--no-decrypt-drm（false）时完全回到旧形态——
// 不解密、也不因为缺少 DRM 外部件而失败。
func TestPlanDrmDecryptionMatrix(t *testing.T) {
	// 隔离 PATH：本机/CI 若装了 Bento4，缺失臂会被 PATH 兜住而变成假绿。
	t.Setenv("PATH", t.TempDir())

	bothDir := t.TempDir()
	writeAsset(t, bothDir, "mp4decrypt")
	writeAsset(t, bothDir, drm.BundledFileName)
	onlyWvdDir := t.TempDir()
	writeAsset(t, onlyWvdDir, drm.BundledFileName)
	onlyMp4Dir := t.TempDir()
	writeAsset(t, onlyMp4Dir, "mp4decrypt")
	emptyDir := t.TempDir()

	drmResult := &entity.ParsedResult{IsDrm: true, DrmTechType: 2}
	plainResult := &entity.ParsedResult{}

	cases := []struct {
		name       string
		cfg        config.MyOption
		result     *entity.ParsedResult
		exeDir     string
		wantDec    bool
		wantErr    bool
		errKeyword string
	}{
		{"自动解密开启 + 内置件齐备", config.MyOption{DecryptDrm: true}, drmResult, bothDir, true, false, ""},
		{"关掉自动解密（--no-decrypt-drm）→ 回旧形态", config.MyOption{DecryptDrm: false}, drmResult, emptyDir, false, false, ""},
		{"开启但缺 mp4decrypt → 下载前失败", config.MyOption{DecryptDrm: true}, drmResult, onlyWvdDir, true, true, "mp4decrypt"},
		{"开启但缺 device.wvd → 下载前失败", config.MyOption{DecryptDrm: true}, drmResult, onlyMp4Dir, true, true, drm.BundledFileName},
		{"开启 + 手动密钥齐备 → 不需要 device.wvd", config.MyOption{
			DecryptDrm: true, DrmKeyHex: strings.Repeat("ab", 16), DrmKidHex: strings.Repeat("cd", 16),
		}, drmResult, onlyMp4Dir, true, false, ""},
		{"开启但没有 DRM 标记（普通稿件）→ 不解密、不要求外部件", config.MyOption{DecryptDrm: true}, plainResult, emptyDir, false, false, ""},
		{"nil 解析结果也不该 panic", config.MyOption{DecryptDrm: true}, nil, emptyDir, false, false, ""},
	}

	for _, c := range cases {
		plan, err := planDrmDecryption(c.cfg, c.result, c.exeDir)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s：应当报前置检查失败，实际通过（plan=%+v）", c.name, plan)
				continue
			}
			if !drm.IsPrerequisiteError(err) {
				t.Errorf("%s：错误应当是可识别的前置检查错误（调用方据此提前返回）：%v", c.name, err)
			}
			if !strings.Contains(err.Error(), c.errKeyword) {
				t.Errorf("%s：错误文案应当点名 %q：%v", c.name, c.errKeyword, err)
			}
			if !plan.Decrypt {
				t.Errorf("%s：报错时判定仍应表明「本来要解密」（便于日志说明意图）：%+v", c.name, plan)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s：不该报错：%v", c.name, err)
		}
		if plan.Decrypt != c.wantDec {
			t.Errorf("%s：Decrypt = %v, want %v", c.name, plan.Decrypt, c.wantDec)
		}
	}

	// 内置件的来源要能说清（日志/doctor 用）：走程序目录时是 bundled。
	plan, err := planDrmDecryption(config.MyOption{DecryptDrm: true}, drmResult, bothDir)
	if err != nil {
		t.Fatalf("内置件齐备时不该报错：%v", err)
	}
	desc := drm.DescribeAssets(plan.Assets)
	if !strings.Contains(desc, "mp4decrypt="+filepath.Join(bothDir, "mp4decrypt")+"(bundled)") ||
		!strings.Contains(desc, drm.BundledFileName+"="+filepath.Join(bothDir, drm.BundledFileName)+"(bundled)") {
		t.Errorf("应当认出内置件来源：%q", desc)
	}
}

// TestPlanDrmDecryptionExplicitPathsWin 钉住显式路径优先（--mp4decrypt-path/--wvd-path，
// 本地构建没有内置件时的主要出路）。
func TestPlanDrmDecryptionExplicitPathsWin(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	writeAsset(t, dir, "custom-mp4decrypt")
	writeAsset(t, dir, "custom.wvd")

	plan, err := planDrmDecryption(config.MyOption{
		DecryptDrm:     true,
		Mp4decryptPath: filepath.Join(dir, "custom-mp4decrypt"),
		WvdPath:        filepath.Join(dir, "custom.wvd"),
	}, &entity.ParsedResult{IsDrm: true}, t.TempDir())
	if err != nil {
		t.Fatalf("显式路径齐备时不该报错：%v", err)
	}
	if plan.Assets.Mp4decrypt.Source != drm.AssetExplicit || plan.Assets.Wvd.Source != drm.AssetExplicit {
		t.Errorf("显式路径应当是 explicit 来源：%+v", plan.Assets)
	}
}

// TestDecryptPhaseUsesSameMp4decryptAsPrecheck 是 t58 的回归：解密期与前置检查必须解析出**同一个**
// mp4decrypt 可执行文件。
//
// 只有内置件时尤其重要（发布包形态：mp4decrypt 与二进制同目录、不在 PATH 上）——旧的解密期口径
// drm.FindMp4decrypt 只有「显式 + PATH」两档，会在这时找不到：前置检查通过，真解密却报
// 「未找到 mp4decrypt」。用例末尾直接反证了这一点（隔离 PATH 后 FindMp4decrypt 返回空），
// 所以「两边一致」这条断言不是空转。
//
// 变异验证：把 decryptDrm 里的 resolveDecryptMp4decrypt 换回 drm.FindMp4decrypt → 本用例红。
func TestDecryptPhaseUsesSameMp4decryptAsPrecheck(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // 隔离 PATH：本机/CI 装了 Bento4 会掩盖差异
	exeDir := t.TempDir()
	bundled := filepath.Join(exeDir, "mp4decrypt")
	// 发布包形态：两个件都在二进制旁边（程序目录），都不在 PATH 上、也没给显式路径。
	writeAsset(t, exeDir, "mp4decrypt")
	writeAsset(t, exeDir, drm.BundledFileName)

	cfg := config.MyOption{DecryptDrm: true}
	plan, err := planDrmDecryption(cfg, &entity.ParsedResult{IsDrm: true}, exeDir)
	if err != nil {
		t.Fatalf("只有内置件时前置检查应当通过：%v", err)
	}
	if plan.Assets.Mp4decrypt.Source != drm.AssetBundled || plan.Assets.Mp4decrypt.Path != bundled {
		t.Fatalf("前置检查应当解析到内置件：%+v", plan.Assets.Mp4decrypt)
	}

	// 解密期（带上前置检查的解析结果）→ 同一个路径。
	if got := resolveDecryptMp4decrypt(cfg, exeDir, plan.Assets.Mp4decrypt.Path); got != bundled {
		t.Errorf("解密期用的是另一个路径：got %q, want %q（必须与前置检查同源）", got, bundled)
	}
	// 没带前置结果时也要自己解析到内置件（同一套 ResolveAssets 语义，而不是回到旧窄口径）。
	if got := resolveDecryptMp4decrypt(cfg, exeDir, ""); got != bundled {
		t.Errorf("兜底解析也应当认程序目录里的内置件：got %q, want %q", got, bundled)
	}
	// 显式路径仍然优先（本地构建没有内置件时的主要出路）。
	otherDir := t.TempDir()
	explicit := filepath.Join(otherDir, "my-mp4decrypt")
	writeAsset(t, otherDir, "my-mp4decrypt")
	cfgExplicit := cfg
	cfgExplicit.Mp4decryptPath = explicit
	if got := resolveDecryptMp4decrypt(cfgExplicit, exeDir, ""); got != explicit {
		t.Errorf("显式 --mp4decrypt-path 应当优先：got %q, want %q", got, explicit)
	}
	// 反证（t58 的缺陷本体）：旧口径在隔离 PATH 后对内置件一无所知。
	if got := drm.FindMp4decrypt(""); got != "" {
		t.Errorf("FindMp4decrypt 只有「显式+PATH」，隔离 PATH 后不该找到：%q", got)
	}
}

// TestDecryptDrmFindsBundledMp4decrypt 是 t58 的**行为级**回归（直接驱动解密期，而不是只测解析函数）：
// 发布包形态（mp4decrypt 与二进制同目录、不在 PATH、没有显式 --mp4decrypt-path）下，
// decryptDrm 必须能找到它并真的调用（假可执行文件 dump argv + 写输出）。
//
// 旧实现解密期用 drm.FindMp4decrypt（只有「显式 + PATH」）→ 这里会直接报「未找到 mp4decrypt」。
//
// 变异验证：把 decryptDrm 里的 resolveDecryptMp4decrypt 换回 drm.FindMp4decrypt → 本用例红。
func TestDecryptDrmFindsBundledMp4decrypt(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // 隔离 PATH：否则本机装的 Bento4 会让这条断言失去意义
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	// 假 mp4decrypt：dump argv，并按第 4 个参数（输出文件）写内容——DecryptStream 用输出替换输入。
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nprintf 'decrypted' > \"$4\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "mp4decrypt"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	// 关键设计：让 appDirFunc 指向**另一个**没有任何内置件的目录——这样「解密期成功」
	// 只可能来自调用方传进来的那份解析结果（即前置检查的那一份），而不是自己又猜了一次。
	emptyDir := t.TempDir()
	origAppDir := appDirFunc
	appDirFunc = func() string { return emptyDir }
	defer func() { appDirFunc = origAppDir }()
	bundled := filepath.Join(dir, "mp4decrypt")

	video := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(video, []byte("still-encrypted"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.MyOption{
		DecryptDrm: true,
		DrmKeyHex:  strings.Repeat("ab", 16),
		DrmKidHex:  strings.Repeat("cd", 16),
	}
	w := &Workflow{Cfg: cfg}
	if err := w.decryptDrm(context.Background(), &entity.ParsedResult{IsDrm: true}, video, "", bundled); err != nil {
		t.Fatalf("解密期应当用传进来的那份解析结果（前置检查解析到的内置 mp4decrypt）：%v", err)
	}

	// 反向一脚：**不带**解析结果、且程序目录里没有内置件时，必须如实报错（而不是悄悄放过）。
	// 这同时证明上一条成功确实来自传入的路径。
	plain := filepath.Join(dir, "plain.mp4")
	if err := os.WriteFile(plain, []byte("still-encrypted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.decryptDrm(context.Background(), &entity.ParsedResult{IsDrm: true}, plain, "", ""); err == nil {
		t.Error("既没有解析结果、程序目录也没有内置件时，应当报「未找到 mp4decrypt」")
	}

	got, err := os.ReadFile(video)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "decrypted" {
		t.Errorf("输入应当被解密产物替换：%q", got)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("内置 mp4decrypt 没被调用（找不到 argv dump）：%v", err)
	}
	args := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(args) < 4 || args[0] != "--key" {
		t.Errorf("argv 形态不对：%q", args)
	}
}

// TestResolveDecryptMp4decryptPrefersPrechecked 钉住「prechecked 优先」（t59 指出的回归网缺口）
// —— 前一条用例里 prechecked 恰好等于第二级兜底算出的同一路径，撤掉优先级判定也能绿；
// 本用例用两者不同的场景：prechecked 指向他处、exeDir 里没有内置件。
// 变异验证：把 resolveDecryptMp4decrypt 的第一级判定改成恒假 → 本用例红。
func TestResolveDecryptMp4decryptPrefersPrechecked(t *testing.T) {
	dir := t.TempDir()
	prechecked := filepath.Join(dir, "prechecked-mp4decrypt")
	if err := os.WriteFile(prechecked, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	exeDir := filepath.Join(dir, "exe") // 不含 mp4decrypt
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.MyOption{}
	got := resolveDecryptMp4decrypt(cfg, exeDir, prechecked)
	if got != prechecked {
		t.Fatalf("prechecked 应优先返回：got %q, want %q", got, prechecked)
	}
}
