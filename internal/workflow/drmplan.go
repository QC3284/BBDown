package workflow

import (
	"github.com/QC3284/BBDown-go/internal/config"
	"github.com/QC3284/BBDown-go/internal/drm"
	"github.com/QC3284/BBDown-go/internal/entity"
)

// ---- DRM 自动解密判定 + 下载前前置检查（t49，吸收上游 1.7.1）----
//
// 判定做成纯函数（输入：配置 + 解析结果 + 程序目录；输出：要不要解密、外部件解析结果），
// 这样开关矩阵能在离线用例里逐格断言，不必真跑一次加密下载。

// drmPlan 是一次解析结果的解密判定。
type drmPlan struct {
	// Decrypt 为真表示这次要（在下载后）解密：配置开着自动解密、且解析结果标记了 DRM。
	Decrypt bool
	// Assets 是前置检查用到的外部件解析结果（缺失时错误文案与 doctor 都用它）。
	Assets drm.Assets
}

// planDrmDecryption 是「自动检测 + 自动解密」的判定（默认开启由 CLI 侧的开关决定，
// 这里只认 Cfg.DecryptDrm 这一个开关，保持可测且与 --no-decrypt-drm 语义正交）：
//
//   - 开关关闭（--no-decrypt-drm / 旧形态）：不解密、不做任何前置检查——与改前逐字一致；
//   - 开关打开且解析结果带 DRM 标记：要解密，并立刻做**下载前**前置检查（缺 mp4decrypt
//     或 device.wvd 时在这里失败，而不是等整段视频下完才在解密阶段报错）；
//   - 开关打开但内容没有 DRM：不解密（也不要求外部件存在，普通下载不该被 DRM 依赖拦住）。
func planDrmDecryption(cfg config.MyOption, result *entity.ParsedResult, exeDir string) (drmPlan, error) {
	plan := drmPlan{Assets: drm.ResolveAssets(exeDir, cfg.Mp4decryptPath, cfg.WvdPath)}
	if !cfg.DecryptDrm || result == nil || !result.IsDrm {
		return plan, nil
	}
	plan.Decrypt = true
	if err := drm.CheckDecryptPrerequisites(plan.Assets, cfg.DrmKeyHex, cfg.DrmKidHex); err != nil {
		return plan, err
	}
	return plan, nil
}
