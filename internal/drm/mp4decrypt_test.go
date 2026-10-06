package drm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeMp4decrypt 写一个假 mp4decrypt：把 argv 逐行落盘，并按「第 4 个参数是输出文件」写一点
// 内容，让 DecryptStream 的产物检查通过。仓库纪律：外部工具一律用假可执行文件 dump argv，
// 不真调用 Bento4（本机也装不出 Bento4）。
// 跨平台（CI windows 红过一次）：Windows 上用 .cmd 批处理（Go 的 exec 自动经 cmd.exe 运行），
// 其余平台用 sh 脚本；两种实现的 argv 落盘口径一致，readArgs 断言不变。
func fakeMp4decrypt(t *testing.T, dir, argsFile, outContent, stderrText string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return fakeMp4decryptCmd(t, dir, argsFile, outContent, stderrText, exitCode)
	}
	script := filepath.Join(dir, "fake-mp4decrypt")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > '" + argsFile + "'\n"
	if outContent != "" {
		body += "printf '" + outContent + "' > \"$4\"\n"
	}
	if stderrText != "" {
		body += "printf '%s\\n' '" + stderrText + "' 1>&2\n"
	}
	if exitCode == 0 {
		body += "exit 0\n"
	} else {
		body += "exit 1\n"
	}
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// fakeMp4decryptCmd 是 Windows 版假可执行文件（.cmd）：把参数逐行回显进 argsFile。
// %* 展开不保空格边界，但本包用例的参数（--key kid:key 与路径）都不含空格，足够驱动断言。
func fakeMp4decryptCmd(t *testing.T, dir, argsFile, outContent, stderrText string, exitCode int) string {
	t.Helper()
	script := filepath.Join(dir, "fake-mp4decrypt.cmd")
	body := "@echo off\r\n" +
		"(for %%a in (%*) do @echo %%a) > \"" + argsFile + "\"\r\n"
	if outContent != "" {
		body += "> \"%4\" echo " + outContent + "\r\n"
	}
	if stderrText != "" {
		body += ">&2 echo " + stderrText + "\r\n"
	}
	if exitCode == 0 {
		body += "exit /b 0\r\n"
	} else {
		body += "exit /b 1\r\n"
	}
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return script
}

func readArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("假可执行文件没被调用（找不到 argv 文件）: %v", err)
	}
	// .cmd 批处理的回显是 CRLF 行尾，逐行去掉 \r（linux 上无害）。
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}

// TestDecryptStreamUsesKeyArgument 钉住 t45 的修法（对齐上游 1.7.2）：
// 调用形态必须是 mp4decrypt --key <kid>:<key> <input> <output>，
// **不能**再出现 --key-file（Bento4 没有这个选项，会以 ERROR: unexpected argument 退出 1）。
//
// 变异验证：把参数改回 --key-file <path> → 本用例红（argv 形态断言）。
func TestDecryptStreamUsesKeyArgument(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := fakeMp4decrypt(t, dir, argsFile, "decrypted-bytes", "", 0)

	input := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(input, []byte("encrypted"), 0o644); err != nil {
		t.Fatal(err)
	}

	const kid = "00112233445566778899aabbccddeeff"
	const key = "ffeeddccbbaa99887766554433221100"
	if err := DecryptStream(context.Background(), script, kid, key, input, time.Minute); err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}

	args := readArgs(t, argsFile)
	want := []string{"--key", kid + ":" + key, input, input + ".dec"}
	if len(args) != len(want) {
		t.Fatalf("argv = %q, want %q", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q（完整 argv：%q）", i, args[i], want[i], args)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "key-file") {
			t.Errorf("argv 里不该再出现 --key-file：%q", args)
		}
	}
	kv := args[1]
	parts := strings.Split(kv, ":")
	if len(parts) != 2 || len(parts[0]) != 32 || len(parts[1]) != 32 {
		t.Fatalf("--key 的值应当是 <32hex>:<32hex>，实际 %q", kv)
	}
	if kv != strings.ToLower(kv) {
		t.Errorf("--key 的值应当是小写 hex，实际 %q", kv)
	}

	got, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "decrypted-bytes" {
		t.Errorf("输入没有被解密产物替换：%q", got)
	}
	if _, err := os.Stat(input + ".dec"); !os.IsNotExist(err) {
		t.Errorf("不该留下 .dec 临时文件（err=%v）", err)
	}
}

// TestDecryptStreamWritesNoTempKeyFile 钉住「不再把密钥落盘」：
// 把 TMPDIR 指到一个空目录，调用后它必须仍然是空的（旧实现会在这里留一个 bbdown-key-*.tmp）。
func TestDecryptStreamWritesNoTempKeyFile(t *testing.T) {
	dir := t.TempDir()
	sysTmp := filepath.Join(dir, "sys-tmp")
	if err := os.MkdirAll(sysTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", sysTmp)

	argsFile := filepath.Join(dir, "args.txt")
	script := fakeMp4decrypt(t, dir, argsFile, "x", "", 0)
	input := filepath.Join(dir, "audio.m4a")
	if err := os.WriteFile(input, []byte("encrypted"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DecryptStream(context.Background(), script, strings.Repeat("ab", 16), strings.Repeat("cd", 16), input, time.Minute); err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}
	entries, err := os.ReadDir(sysTmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("解密不该产生任何临时文件（旧实现会写 bbdown-key-*.tmp），实际：%v", names)
	}
	for _, a := range readArgs(t, argsFile) {
		if strings.Contains(a, "bbdown-key") {
			t.Errorf("argv 里出现了临时 key 文件：%q", a)
		}
	}
}

// TestDecryptStreamLowercasesHex 钉住 --key 的值统一小写（Bento4/上游口径）。
func TestDecryptStreamLowercasesHex(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := fakeMp4decrypt(t, dir, argsFile, "x", "", 0)
	input := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(input, []byte("e"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DecryptStream(context.Background(), script,
		"00112233445566778899AABBCCDDEEFF", "FFEEDDCCBBAA99887766554433221100", input, time.Minute); err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}
	args := readArgs(t, argsFile)
	if args[1] != "00112233445566778899aabbccddeeff:ffeeddccbbaa99887766554433221100" {
		t.Errorf("--key 的值没有规范成小写：%q", args[1])
	}
}

// TestDecryptStreamFailureCleansUp 失败路径：mp4decrypt 非零退出时把 stderr 带进错误，
// 并清掉半成品 .dec（不能把「解密失败」伪装成成功）。
func TestDecryptStreamFailureCleansUp(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := fakeMp4decrypt(t, dir, argsFile, "partial", "ERROR: unexpected argument", 1)
	input := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(input, []byte("encrypted"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := DecryptStream(context.Background(), script, strings.Repeat("ab", 16), strings.Repeat("cd", 16), input, time.Minute)
	if err == nil {
		t.Fatal("mp4decrypt 退出 1 时应当返回错误")
	}
	if !strings.Contains(err.Error(), "mp4decrypt failed") || !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("错误里应当带 mp4decrypt 的 stderr：%v", err)
	}
	if _, statErr := os.Stat(input + ".dec"); !os.IsNotExist(statErr) {
		t.Errorf("失败后不该留下半成品 .dec：%v", statErr)
	}
	if got, _ := os.ReadFile(input); string(got) != "encrypted" {
		t.Errorf("失败时输入不该被替换：%q", got)
	}
}

// TestDecryptStreamInputValidation 钉住既有的输入校验（缺 kid/key、输入不存在、空输入直接跳过）。
func TestDecryptStreamInputValidation(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(input, []byte("e"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DecryptStream(context.Background(), "mp4decrypt", "", "cd", input, time.Minute); err == nil {
		t.Error("缺 kid 应当报错")
	}
	if err := DecryptStream(context.Background(), "mp4decrypt", "ab", "", input, time.Minute); err == nil {
		t.Error("缺 key 应当报错")
	}
	if err := DecryptStream(context.Background(), "mp4decrypt", "ab", "cd", filepath.Join(dir, "nope.mp4"), time.Minute); err == nil {
		t.Error("输入不存在应当报错")
	}
	if err := DecryptStream(context.Background(), "mp4decrypt", "ab", "cd", "", time.Minute); err != nil {
		t.Errorf("空输入应当直接跳过（无 DRM 的稿件就是这条路径）：%v", err)
	}
}

// TestMp4decryptRoundTripWithBento4 是真机往返：ffmpeg 造一段小视频 → mp4encrypt 加密 →
// DecryptStream 解密 → 比对**解码后的帧**（frame md5）。
//
// 为什么不是逐字节：CENC 加密会重写容器（加 tenc/sinf 等 box），解密后的容器布局与原始文件
// 不保证逐字节相同；媒体内容一致才是「解密正确」的判据。
// 本机没有 Bento4 时按仓库纪律 t.Skip 写明原因（不靠「本机跑得过」）。
func TestMp4decryptRoundTripWithBento4(t *testing.T) {
	encrypt, errEnc := exec.LookPath("mp4encrypt")
	decrypt, errDec := exec.LookPath("mp4decrypt")
	ffmpeg, errFF := exec.LookPath("ffmpeg")
	if errEnc != nil || errDec != nil {
		t.Skipf("本机没有 Bento4（mp4encrypt/mp4decrypt 不在 PATH）——真机往返待有 Bento4 的机器上跑；本次已用假可执行文件钉住 argv 形态。mp4encrypt=%v mp4decrypt=%v", errEnc, errDec)
	}
	if errFF != nil {
		t.Skipf("有 Bento4 但没有 ffmpeg（造夹具需要它）：%v", errFF)
	}

	dir := t.TempDir()
	orig := filepath.Join(dir, "plain.mp4")
	gen := exec.Command(ffmpeg, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=10:duration=1",
		"-pix_fmt", "yuv420p", "-c:v", "libx264", orig)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg 造夹具失败（本机编码器不可用）：%v: %s", err, out)
	}

	const kid = "00112233445566778899aabbccddeeff"
	const key = "ffeeddccbbaa99887766554433221100"
	encrypted := filepath.Join(dir, "enc.mp4")
	enc := exec.Command(encrypt, "--method", "MPEG-CENC", "--key", "1:"+key+":cenc",
		"--property", "1:KID:"+kid, orig, encrypted)
	if out, err := enc.CombinedOutput(); err != nil {
		t.Skipf("mp4encrypt 加密失败（本机 Bento4 版本/参数不支持）：%v: %s", err, out)
	}

	if err := DecryptStream(context.Background(), decrypt, kid, key, encrypted, 30*time.Second); err != nil {
		t.Fatalf("DecryptStream 真机往返失败: %v", err)
	}
	if info, err := os.Stat(encrypted); err != nil || info.Size() == 0 {
		t.Fatalf("解密产物无效: %v", err)
	}

	frameMD5 := func(path string) string {
		out, err := exec.Command(ffmpeg, "-v", "error", "-i", path, "-f", "md5", "-").Output()
		if err != nil {
			t.Fatalf("ffmpeg 取样 %s 失败: %v", path, err)
		}
		return string(out)
	}
	if got, want := frameMD5(encrypted), frameMD5(orig); got != want {
		t.Errorf("解密后的解码帧与原始文件不一致：got %q, want %q", got, want)
	}
}
