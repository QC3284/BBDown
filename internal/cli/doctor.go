package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/download"
	"github.com/QC3284/BBDown/internal/muxer"
	"github.com/QC3284/BBDown/internal/util"
)

// bbdown doctor —— 本仓特色功能：一条命令定位「为什么下不动」。
//
// 这个工具的依赖面比一般下载器宽：外部混流工具（ffmpeg/mp4box）、登录 Cookie、B 站接口可达性、
// 风控（412）、以及输出目录可写/磁盘余量。排障时任何一环都可能卡住，而用户看到的往往只是一句
// 失败。doctor 把这些点逐条探一遍并给出可执行的下一步。两条 C# 接手线都没有这个命令。

// doctorCmd 是 bbdown doctor 子命令。退出码：有失败项（符号 x）则为 1，便于脚本判读。
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "自检：外部工具、输出目录、接口与登录态",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := buildMyOption()
		client := buildHTTPClient(cfg)
		// 中断语义由 Execute 统一安装（见 interrupt.go）：这里只取那份 ctx，不再自己注册信号，
		// 否则第二次 Ctrl+C 会被 signal 层吞掉。
		ctx := commandContext(cmd)
		// JSON 是机读契约：写 cmd 的输出流（纯 stdout、无时间戳/无色码），供脚本与监控解析。
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			// 机读模式：日志让位到 stderr（stdout 只留 JSON），RunE 返回时还原。
			restoreLogs := yieldLogsToStderr(true)
			defer restoreLogs()
			if code := runDoctorJSON(ctx, cfg, client, cmd.OutOrStdout()); code != 0 {
				return fmt.Errorf("自检未通过（按上面的失败项处理）")
			}
			return nil
		}
		// 人类可读输出走 util 日志：这样 --log-file 也能抓到自检结果（见 runDoctor）。
		if code := runDoctor(ctx, cfg, client); code != 0 {
			return fmt.Errorf("自检未通过（按上面的失败项处理）")
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

// runDoctor 跑完所有自检并按列对齐的表格输出，返回退出码：有 fail 返回 1，否则 0。
//
// 输出走 util 日志而不是直接写 os.Stdout：doctor 此前绕过了 logger，用户带 --log-file 跑完
// 自检去提 issue 时，日志文件里恰恰缺了 fail 那几行。每个**物理行**单独过一次日志（折行后的
// 续行同色），而不是把多行文本塞进一条日志——util 的 SanitizeLogString 会把换行压成空格。
func runDoctor(ctx context.Context, cfg config.MyOption, client *util.HTTPClient) int {
	results, code := runDoctorResults(ctx, cfg, client)
	for _, row := range renderDoctorRows(results) {
		// 状态**已经是表里的第一列**（+ / ! / x），所以走 LogTagged 而不是 LogWarn/LogError：
		// 后者会在行首再插一个 ⚠/✗，既把名称列推歪，又变成双重标注。等级改用状态色表达。
		tag := util.TagText
		switch row.Level {
		case "fail":
			tag = util.TagError
		case "warn":
			tag = util.TagWarn
		}
		for _, line := range row.Lines {
			util.LogTagged(tag, "%s", line)
		}
	}
	if code == 0 {
		util.Log("自检通过：没有发现阻塞性问题。")
	} else {
		util.LogError("存在阻塞性问题：先按上面标 x 的失败项处理，仍不行请带上本输出提 issue。")
	}
	return code
}

// doctorRow 是一个自检项排版后的结果：结论等级 + 若干物理行（首行是「符号 名称 详情」，
// 详情超宽时续行缩进到详情列）。
type doctorRow struct {
	Level string
	Lines []string
}

// doctorWrapWidth 是一条自检消息（不含日志时间戳）允许的最大显示列数。
//
// 取固定值而不是读终端宽度：自检输出同时进终端与 --log-file，日志文件里没有「终端宽度」，
// 固定宽度才能让两处折行与缩进完全一致；用例也能直接收窄它，把折行路径钉死。
var doctorWrapWidth = 100

// doctorMarkWidth 是状态符号的显示列数：三档符号都取 1 列，名称列的起点才固定。
const doctorMarkWidth = 1

// renderDoctorRows 把自检结果排成列对齐的表格：状态符号 + 名称列（按本次最长名称对齐）+
// 详情列；详情超过 doctorWrapWidth 时折行，续行缩进到详情列，仍能看出属于哪一项。
//
// 名称列宽按**显示列**算而不是 rune 数：名称中英混排（"ffmpeg/mp4box" 与 "接口/登录态"），
// 按 rune 补空格在终端里对不齐。改前每行是「[ok] 名称: 详情」，详情长短不一没法扫。
//
// 宽度表只有一份：列宽与补白一律走 download.DisplayWidth/download.PadDisplay——那里同时服务流清单
// 与任务卡，doctor 不再养第二张同名表（此前三处各一份，改表要三处一起改，漏一处就错位）。
func renderDoctorRows(results []doctorResult) []doctorRow {
	nameWidth := 0
	for _, res := range results {
		if w := download.DisplayWidth(res.Name); w > nameWidth {
			nameWidth = w
		}
	}
	detailCol := doctorMarkWidth + 1 + nameWidth + 2
	detailWidth := doctorWrapWidth - detailCol
	if detailWidth < 1 {
		detailWidth = 1
	}
	rows := make([]doctorRow, 0, len(results))
	for _, res := range results {
		head := doctorMark(res.Level) + " " + download.PadDisplay(res.Name, nameWidth) + "  "
		chunks := wrapDisplay(res.Detail, detailWidth)
		if len(chunks) == 0 {
			chunks = []string{""}
		}
		lines := make([]string, 0, len(chunks))
		for i, chunk := range chunks {
			line := head + chunk
			if i > 0 {
				line = strings.Repeat(" ", detailCol) + chunk
			}
			if chunk == "" {
				line = strings.TrimRight(line, " ") // 详情为空时不留行尾空格
			}
			lines = append(lines, line)
		}
		rows = append(rows, doctorRow{Level: res.Level, Lines: lines})
	}
	return rows
}

// doctorMark 把结论等级映射成状态符号：+ 通过 / ! 警告 / x 失败。
//
// 用单字符符号而不是旧的 [ok]/[warn]/[fail]：括号词每档宽度不同（旧实现得给 [ok] 补两个空格
// 才勉强对齐），符号短、噪声少；等级另由颜色与末尾结论行区分。
func doctorMark(level string) string {
	switch level {
	case "fail":
		return "x"
	case "warn":
		return "!"
	default:
		return "+"
	}
}

// wrapDisplay 把文本按显示列宽折成若干行：只在整字符边界断开（详情中英混排、以无空格的中文
// 为主，不做词法断行）；断行处若是空格就丢掉，续行的缩进由调用方补。单字符本身超宽时允许该行
// 变宽（与 download.PadDisplay 同一取舍：不丢信息）。空串返回 nil。
//
// 单字符宽度用 download.DisplayWidth(string(r))：宽度表只有一份，这里不再自带 rune 版副本；
// doctor 的详情最多几十个 rune，逐字符折算的开销可忽略。
func wrapDisplay(s string, width int) []string {
	if s == "" {
		return nil
	}
	if width < 1 {
		return []string{s}
	}
	var (
		out  []string
		cur  strings.Builder
		used int
	)
	for _, r := range s {
		w := download.DisplayWidth(string(r))
		if used+w > width && cur.Len() > 0 {
			out = append(out, strings.TrimRight(cur.String(), " "))
			cur.Reset()
			used = 0
			if r == ' ' {
				continue
			}
		}
		cur.WriteRune(r)
		used += w
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
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
