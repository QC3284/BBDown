package drm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// FindMp4decrypt resolves the mp4decrypt binary: explicit path first, then PATH lookup.
func FindMp4decrypt(explicitPath string) string {
	if explicitPath != "" {
		if info, err := os.Stat(explicitPath); err == nil && !info.IsDir() {
			return explicitPath
		}
	}
	if p, err := exec.LookPath("mp4decrypt"); err == nil {
		return p
	}
	return ""
}

// DecryptStream decrypts an encrypted media file in place, replacing the input
// atomically with the decrypted output. Timeout caps the external process
// (upstream reuses the muxer timeout for this).
func DecryptStream(ctx context.Context, mp4decryptPath, kidHex, keyHex, input string, timeout time.Duration) error {
	if input == "" {
		return nil
	}
	if kidHex == "" || keyHex == "" {
		return fmt.Errorf("decrypt: key or kid missing")
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("decrypt: input not found: %w", err)
	}
	output := input + ".dec"
	_ = os.Remove(output)

	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Bento4 的 mp4decrypt **只有** --key <id>:<key> 一个传密钥的选项（上游 1.7.2 的修法）。
	//
	// 这里曾经写成 --key-file <keyfile>（先写临时文件、再把路径传进去）：那个选项
	// mp4decrypt 根本不认，会直接以 「ERROR: unexpected argument」 退出（退出码 1）——
	// 也就是说 DRM 解密路径**必然失败**；而且那份临时文件会把 kid:key 明文留在磁盘上。
	// 现在密钥直接作参数传（hex 统一小写，与 Bento4 输出/上游一致），不再产生任何临时文件。
	// 权衡（与上游一致）：密钥会短暂出现在本机进程命令行（/proc/<pid>/cmdline）里，仍优于旧实现
	// 的「明文落盘」；将来若在意，可评估 stdin 或环境变量传密钥（上游也未做）。
	keyArg := strings.ToLower(kidHex) + ":" + strings.ToLower(keyHex)
	cmd := exec.CommandContext(runCtx, mp4decryptPath, "--key", keyArg, input, output)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(output)
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("mp4decrypt timed out after %v", timeout)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("mp4decrypt failed: %v: %s", err, string(stderr))
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(output)
		return fmt.Errorf("mp4decrypt exited 0 but produced no valid output")
	}

	// Atomically replace the input with the decrypted output.
	if err := os.Remove(input); err != nil {
		_ = os.Remove(output)
		return fmt.Errorf("decrypt: remove original: %w", err)
	}
	if err := os.Rename(output, input); err != nil {
		return fmt.Errorf("decrypt: replace with decrypted: %w", err)
	}
	return nil
}
