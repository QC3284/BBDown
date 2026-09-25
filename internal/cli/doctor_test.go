package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
)

// bbdown doctor（本仓特色功能）：自检项要能离线覆盖——接口用假 hosts，外部工具用假可执行文件。
//
// 变异验证：把 checkAPIAndLogin 改成恒返回 ok → 412 用例变红；把 checkMuxTools 的 ffmpeg 检查删掉
// → 「找不到 ffmpeg」用例变红。
func doctorWith(t *testing.T, stubTools bool, nav string, status int) (string, int) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(nav))
	}))
	defer srv.Close()

	cfg := config.DefaultMyOption()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.WorkDir = t.TempDir()
	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)

	// 工具检查依赖机器上装没装 ffmpeg（CI runner 通常没有）：测接口/登录态的用例要把它换成恒 ok 的桩，
	// 否则用例随环境红绿（真红过一次）；专门测工具检查的用例传 stubTools=false 走真实现。
	if stubTools {
		orig := doctorChecks
		doctorChecks = []func(context.Context, config.MyOption, *util.HTTPClient) doctorResult{
			func(context.Context, config.MyOption, *util.HTTPClient) doctorResult {
				return doctorResult{"ffmpeg/mp4box", "ok", "桩"}
			},
			checkWorkDir,
			checkAPIAndLogin,
		}
		t.Cleanup(func() { doctorChecks = orig })
	}

	var buf bytes.Buffer
	code := runDoctor(context.Background(), cfg, client, &buf)
	return buf.String(), code
}

func TestDoctorReportsLoggedInState(t *testing.T) {
	nav := strings.ReplaceAll("{'code':0,'data':{'isLogin':true,'uname':'tester','vipStatus':1}}", "'", string('"'))
	out, code := doctorWith(t, true, nav, 0)
	if !strings.Contains(out, "已登录 tester") {
		t.Errorf("应当报告登录态与用户名，实际输出：%s", out)
	}
	if !strings.Contains(out, "大会员: 是") {
		t.Errorf("应当报告大会员状态，实际输出：%s", out)
	}
	if code != 0 {
		t.Errorf("全部通过时退出码应为 0，实际 %d\n%s", code, out)
	}
}

func TestDoctorWarnsWhenNotLoggedIn(t *testing.T) {
	nav := strings.ReplaceAll("{'code':0,'data':{'isLogin':false}}", "'", string('"'))
	out, code := doctorWith(t, true, nav, 0)
	if !strings.Contains(out, "未登录") || !strings.Contains(out, "[warn]") {
		t.Errorf("未登录应当是 warn 并给出登录指引，实际输出：%s", out)
	}
	if code != 0 {
		t.Errorf("只有 warn 时退出码应为 0（warn 不阻塞），实际 %d", code)
	}
}

func TestDoctorFailsOnRiskControl(t *testing.T) {
	out, code := doctorWith(t, true, "{}", http.StatusPreconditionFailed)
	// 断言必须钉住 doctor 自己的分支（提示语里也有「风控」两字，只查这两个字会假绿——实测过）。
	if !strings.Contains(out, "被风控拦截(HTTP 412)") || !strings.Contains(out, "自动轮换 UA") {
		t.Errorf("412 应当被 doctor 识别为风控并给出处置建议，实际输出：%s", out)
	}
	if code != 1 {
		t.Errorf("有 fail 项时退出码应为 1，实际 %d", code)
	}
}

func TestDoctorFailsWhenFFmpegMissing(t *testing.T) {
	// 这条要**走真检查**：把 ffmpeg 指到一个必然不存在的程序名，避免依赖机器装没装。
	setMuxerFFmpeg(t, "bbdown-doctor-no-such-ffmpeg")

	nav := strings.ReplaceAll("{'code':0,'data':{'isLogin':true,'uname':'t','vipStatus':0}}", "'", string('"'))
	out, code := doctorWith(t, false, nav, 0)
	if !strings.Contains(out, "[fail]") || !strings.Contains(out, "未找到") {
		t.Errorf("缺少 ffmpeg 应当 fail，实际输出：%s", out)
	}
	if code != 1 {
		t.Errorf("退出码应为 1，实际 %d", code)
	}
}
