package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/muxer"
	"github.com/QC3284/BBDown/internal/util"
)

// bbdown doctor —— 本仓特色功能：一条命令定位「为什么下不动」。
//
// 这个工具的依赖面比一般下载器宽：外部混流工具（ffmpeg/mp4box）、登录 Cookie、B 站接口可达性、
// 风控（412）、以及输出目录可写/磁盘余量。排障时任何一环都可能卡住，而用户看到的往往只是一句
// 失败。doctor 把这些点逐条探一遍并给出可执行的下一步。两条 C# 接手线都没有这个命令。

// doctorCmd 是 bbdown doctor 子命令。退出码：有 [fail] 项则为 1，便于脚本判读。
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "自检：外部工具、输出目录、接口与登录态",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := buildMyOption()
		client := buildHTTPClient(cfg)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		// JSON 是机读契约：写 cmd 的输出流（纯 stdout、无时间戳/无色码），供脚本与监控解析。
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			if code := runDoctorJSON(ctx, cfg, client, cmd.OutOrStdout()); code != 0 {
				return fmt.Errorf("自检未通过（按上面的 [fail] 项处理）")
			}
			return nil
		}
		// 人类可读输出走 util 日志：这样 --log-file 也能抓到自检结果（见 runDoctor）。
		if code := runDoctor(ctx, cfg, client); code != 0 {
			return fmt.Errorf("自检未通过（按上面的 [fail] 项处理）")
		}
		return nil
	},
}

// doctorResult 是一条自检结论。
type doctorResult struct {
	Name   string `json:"name"`
	Level  string `json:"level"` // ok / warn / fail
	Detail string `json:"detail"`
}

// doctorChecks 是自检项，抽成变量以便用例替换（避免测试真的去碰环境）。
var doctorChecks = []func(context.Context, config.MyOption, *util.HTTPClient) doctorResult{
	checkMuxTools,
	checkWorkDir,
	checkAPIAndLogin,
}

// runDoctorResults 跑完所有自检，返回结果与退出码（有 fail 则 1）。
func runDoctorResults(ctx context.Context, cfg config.MyOption, client *util.HTTPClient) ([]doctorResult, int) {
	results := make([]doctorResult, 0, len(doctorChecks))
	code := 0
	for _, check := range doctorChecks {
		res := check(ctx, cfg, client)
		results = append(results, res)
		if res.Level == "fail" {
			code = 1
		}
	}
	return results, code
}

// runDoctorJSON 以 JSON 输出自检结果：给脚本/监控用（特色功能的机读形态）。
func runDoctorJSON(ctx context.Context, cfg config.MyOption, client *util.HTTPClient, out io.Writer) int {
	results, code := runDoctorResults(ctx, cfg, client)
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintf(out, "{\"error\": %q}\n", err.Error())
		return 1
	}
	fmt.Fprintln(out, string(data))
	return code
}

// runDoctor 跑完所有自检并按人类可读格式输出，返回退出码：有 fail 返回 1，否则 0。
//
// 输出走 util 日志而不是直接写 os.Stdout：doctor 此前绕过了 logger，用户带 --log-file 跑完
// 自检去提 issue 时，日志文件里恰恰缺了 [fail] 那几行。形态（[ok]/[warn]/[fail] + 结论行）
// 与改前逐字一致，只多了日志时间戳与分级配色。
func runDoctor(ctx context.Context, cfg config.MyOption, client *util.HTTPClient) int {
	results, code := runDoctorResults(ctx, cfg, client)
	for _, res := range results {
		line := fmt.Sprintf("%s %s: %s", doctorMark(res.Level), res.Name, res.Detail)
		switch res.Level {
		case "fail":
			util.LogError("%s", line)
		case "warn":
			util.LogWarn("%s", line)
		default:
			util.Log("%s", line)
		}
	}
	if code == 0 {
		util.Log("自检通过：没有发现阻塞性问题。")
	} else {
		util.LogError("存在阻塞性问题：先按上面的 [fail] 项处理，仍不行请带上本输出提 issue。")
	}
	return code
}

// doctorMark 把结论等级映射成标记；[ok] 补两个空格，让三档在终端里对齐。
func doctorMark(level string) string {
	return map[string]string{"ok": "[ok]  ", "warn": "[warn]", "fail": "[fail]"}[level]
}

// checkMuxTools 检查混流工具：ffmpeg 必需（含杜比视界支持探测），mp4box 仅在部分场景需要。
func checkMuxTools(ctx context.Context, cfg config.MyOption, _ *util.HTTPClient) doctorResult {
	ffmpeg, err := exec.LookPath(muxer.FFMPEG)
	if err != nil {
		return doctorResult{"ffmpeg", "fail", "未找到（混流必需）；装一个或加 --ffmpeg-path 指定绝对路径"}
	}
	dovi := "未知"
	if muxer.CheckFFmpegDOVI() {
		dovi = "是"
	} else {
		dovi = "否（杜比视界会回退 mp4box）"
	}
	detail := fmt.Sprintf("%s；杜比视界混流: %s", ffmpeg, dovi)
	if _, err := exec.LookPath("mp4box"); err != nil {
		if _, err2 := exec.LookPath("MP4Box"); err2 != nil {
			return doctorResult{"ffmpeg/mp4box", "warn", detail + "；未找到 mp4box（杜比视界/部分裸流合并需要）"}
		}
	}
	return doctorResult{"ffmpeg/mp4box", "ok", detail}
}

// checkWorkDir 检查输出目录可写与磁盘余量。
func checkWorkDir(_ context.Context, cfg config.MyOption, _ *util.HTTPClient) doctorResult {
	dir := cfg.WorkDir
	if dir == "" {
		dir = "."
	}
	probe := fmt.Sprintf("%s/.bbdown-doctor-%d", strings.TrimRight(dir, "/"), time.Now().UnixNano())
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return doctorResult{"输出目录", "fail", fmt.Sprintf("%s 不可写：%v", dir, err)}
	}
	os.Remove(probe)
	if free, err := freeDiskBytes(dir); err == nil {
		if free < 1<<30 {
			return doctorResult{"输出目录", "warn", fmt.Sprintf("%s 可写，但剩余空间仅 %.1f GiB", dir, float64(free)/(1<<30))}
		}
		return doctorResult{"输出目录", "ok", fmt.Sprintf("%s 可写，剩余 %.1f GiB", dir, float64(free)/(1<<30))}
	}
	return doctorResult{"输出目录", "ok", dir + " 可写"}
}

// freeDiskBytes 返回目录所在文件系统的剩余字节（非 Linux/Unix 平台返回错误，由调用方降级）。
func freeDiskBytes(dir string) (uint64, error) {
	if runtime.GOOS == "windows" {
		return 0, fmt.Errorf("unsupported")
	}
	return statfsFree(dir)
}

// checkAPIAndLogin 检查接口可达性与登录态：这是「下不动」最常见的原因（Cookie 过期、412 风控、网络）。
func checkAPIAndLogin(ctx context.Context, cfg config.MyOption, client *util.HTTPClient) doctorResult {
	host := cfg.Host
	if host == "" {
		host = "api.bilibili.com"
	}
	body, err := client.GetWebSource(ctx, fmt.Sprintf("https://%s/x/web-interface/nav", host))
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "412") {
			return doctorResult{"接口/登录态", "fail", "被风控拦截(HTTP 412)：等几分钟再试、换网络出口，或稍后重试（本仓会自动轮换 UA 重试 3 次）"}
		}
		return doctorResult{"接口/登录态", "fail", "请求 " + host + " 失败：" + msg}
	}
	var nav struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			IsLogin   bool   `json:"isLogin"`
			Uname     string `json:"uname"`
			VipStatus int    `json:"vipStatus"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &nav); err != nil {
		return doctorResult{"接口/登录态", "warn", "接口可达但响应无法解析（可能被中间人/镜像改写）：" + err.Error()}
	}
	if !nav.Data.IsLogin {
		return doctorResult{"接口/登录态", "warn", "未登录：仅能拿到低清晰度；扫码登录用 bbdown login"}
	}
	vip := "否"
	if nav.Data.VipStatus > 0 {
		vip = "是"
	}
	return doctorResult{"接口/登录态", "ok", fmt.Sprintf("已登录 %s（大会员: %s）", nav.Data.Uname, vip)}
}
