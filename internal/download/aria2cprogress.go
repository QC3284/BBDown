package download

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// aria2c 的**逐字节进度**（任务 R）：aria2c 是黑盒（只有退出码），但它支持
// --summary-interval=<秒> 周期性打印进度摘要，形如（**走 stdout**，不是 stderr）：
//
//	*** Download Progress Summary as of Sun Oct 05 12:00:00 2026 ***
//	===============================================================================
//	[#7d3f21 16KiB/19MiB(0%) CN:1 DL:16KiB ETA:20m19s]
//	FILE: /path/to/out.bin
//	-------------------------------------------------------------------------------
//	（空行）
//
// 其中方括号那一行就是逐字节进度的全部来源：解析成 aria2Summary，再经既有的
// ProgressEvent/观察者管线发帧（Key=产物路径），SSE 与其它观察者消费者因此不需要任何改动。
//
// **流的选择是这条链路的关键**（t34 修正）：实测摘要块写在 aria2c 的 **stdout** 上，
// 所以挂钩观察者时 stdout 与 stderr **两条流都要接进本泵**（见 downloadWithAria2c）；
// 只接 stderr 会一帧都收不到（这正是上一轮的缺陷）。没有观察者时两条流都不接管，
// aria2c 的输出照旧直接继承父进程。
//
// 三条纪律：
//   - **纯函数**解析：字符串进、数值出，喂假摘要行即可离线测；
//   - **静默降级**：形态不认识就返回零值 + false，不报错、不打断既有事件流
//     （摘要行来自外部进程，它的格式不由我们保证）；
//   - **不吞错误**：只消费识别出的摘要块行（含块尾的空行），其余行原样转发。

// aria2cSummaryInterval 是给 aria2c 的摘要间隔（秒）。只在挂了观察者时启用：没有观察者时
// 摘要只是终端噪声，而且会让 CLI 的 aria2c 路径与改前不再逐字一致。
const aria2cSummaryInterval = 1

// aria2Summary 是一帧 aria2c 摘要（解析器只做字符串 → 数值，不碰 IO/时钟）。
type aria2Summary struct {
	Completed int64   // 已完成字节
	Total     int64   // 总字节（解析器只接受 > 0 的行；0 表示「这一行没给出可用的总量」）
	SpeedBps  float64 // DL 列：字节/秒（该行没给就是 0，与 ProgressEvent 的「未结算」同义）
	// ETA 不进 ProgressEvent（那个契约只有字节/速率，没有剩余时间字段）；解析它是为了
	// 「摘要行给了什么就认什么」，也便于用例按原文逐字段核对。
	ETA int64 // 剩余秒数（该行没给就是 0）
}

// progressEvent 把摘要映射成既有的逐字节进度事件。key 是产物身份（见 ProgressEvent.Key），
// 由调用方给（aria2c 路径就是产物路径），服务端据此把逐文件计数累计成任务级字节数。
func (s aria2Summary) progressEvent(key string) ProgressEvent {
	completed := s.Completed
	if completed < 0 {
		completed = 0 // 负值只可能来自畸形行；契约里 0 是「未知」，负值只会污染累计
	}
	return ProgressEvent{Key: key, Current: completed, Total: s.Total, SpeedBps: s.SpeedBps}
}

// aria2SizeUnits 是摘要行里可能出现的体积单位（**长后缀在前**，匹配时先到先得）。
//
// 二进制（KiB/MiB/GiB/TiB）与十进制（KB/MB/GB/TB）都认：aria2 主用二进制，但不同版本/
// 平台的写法未必一致——认全了才不会把「能认出来的进度」静默丢掉。
var aria2SizeUnits = []struct {
	suffix string
	factor float64
}{
	{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3},
	{"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10},
}

// parseAria2Size 解析摘要里的体积（"20MiB" / "1.5GB" / "512B" / "0B"）。
// 畸形（空、非数字、负号、未知单位）返回 false。
func parseAria2Size(s string) (float64, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return 0, false
	}
	num := v
	factor := float64(1)
	for _, unit := range aria2SizeUnits {
		if len(v) > len(unit.suffix) && strings.HasSuffix(v, unit.suffix) {
			num = v[:len(v)-len(unit.suffix)]
			factor = unit.factor
			break
		}
	}
	if num == v {
		// 没有单位：只接受裸字节的 "512B"（aria2 的小体积写法）。
		if len(v) > 1 && strings.HasSuffix(v, "B") {
			num = v[:len(v)-1]
		} else if !isAria2Digits(num) {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil || f < 0 {
		return 0, false
	}
	return f * factor, true
}

// isAria2Digits 判断是不是纯数字（没有单位时可接受的唯一形态）。
func isAria2Digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseAria2ETA 解析剩余时间（"13s" / "1m20s" / "2h3m4s" → 秒）。
//
// 段顺序与重复都容忍（aria2 只会给规范的降序形式，但这里不必为此判红）；单位缺失（"13"）
// 或出现未知字符一律 false——宁可不报 ETA，也不要报一个错的。
func parseAria2ETA(s string) (int64, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return 0, false
	}
	var total, cur int64
	digits, seenUnit := false, false
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= '0' && c <= '9' {
			cur = cur*10 + int64(c-'0')
			digits = true
			continue
		}
		var mult int64
		switch c {
		case 'h':
			mult = 3600
		case 'm':
			mult = 60
		case 's':
			mult = 1
		default:
			return 0, false
		}
		if !digits {
			return 0, false // "m20s" 这种缺数字的段
		}
		total += cur * mult
		cur, digits, seenUnit = 0, false, true
	}
	if digits || !seenUnit {
		return 0, false // 末尾还有没结算的数字（"1m2"）或整行没有单位
	}
	return total, true
}

// parseAria2SummaryLine 解析 aria2c 的进度行（见文件头）。
//
// 接受：以 [ 开头、] 结尾，第一段是 #<gid>，第二段是 completed/total[(percent%)]。
// 其余字段里只取 DL:（速率）与 ETA:（剩余时间）——CN:（连接数）与百分比不进事件：
// 百分比由字节数推出（事件契约里 percent = downloaded/total），比摘要里的整数值更准。
//
// 返回 false 的形态（一个都不报错、一个都不 panic）：空行、非方括号、没有 gid、没有
// completed/total、体积/速率/时间畸形、总量 <= 0。
func parseAria2SummaryLine(line string) (aria2Summary, bool) {
	s := strings.TrimSpace(strings.TrimRight(line, "\r"))
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return aria2Summary{}, false
	}
	fields := strings.Fields(s[1 : len(s)-1])
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "#") {
		return aria2Summary{}, false
	}
	parts := strings.SplitN(fields[1], "/", 2)
	if len(parts) != 2 {
		return aria2Summary{}, false
	}
	completed, ok := parseAria2Size(parts[0])
	if !ok {
		return aria2Summary{}, false
	}
	totalPart := parts[1]
	if i := strings.IndexByte(totalPart, '('); i >= 0 {
		totalPart = totalPart[:i] // 去掉 "(20%)"：百分比由字节数推出，见上
	}
	total, ok := parseAria2Size(totalPart)
	if !ok || total <= 0 {
		return aria2Summary{}, false
	}

	out := aria2Summary{Completed: int64(completed), Total: int64(total)}
	for _, f := range fields[2:] {
		switch {
		case strings.HasPrefix(f, "DL:"):
			if v, ok := parseAria2Size(strings.TrimPrefix(f, "DL:")); ok {
				out.SpeedBps = v
			}
		case strings.HasPrefix(f, "ETA:"):
			if v, ok := parseAria2ETA(strings.TrimPrefix(f, "ETA:")); ok {
				out.ETA = v
			}
		}
	}
	return out, true
}

// aria2ProgressDownstream 是「非摘要行」的转发目标（默认 stderr）。变量而非常量：
// 用例据此断言「摘要块被消费、错误行照原样转发」。
var aria2ProgressDownstream = func() io.Writer { return os.Stderr }

// isAria2SummaryBlockLine 判断这一行是不是 aria2c 摘要块的一部分（与解析成败无关）。
//
// 摘要块长这样（--summary-interval 每秒一块）：
//
//	*** Download Progress Summary as of Sun Mar 10 12:00:00 2024 ***
//	===============================================================================
//	[#7d3f21 20MiB/100MiB(20%) CN:3 DL:6.2MiB ETA:13s]
//	FILE: /path/to/file.mp4
//	-------------------------------------------------------------------------------
//
// 识别出来的行会被**消费掉**（信息已经进了进度事件流，再往 stderr 打一遍就是每秒一屏
// 噪声）；解析失败的方括号行也算摘要块行（形态变了 = 静默降级，不能变成刷屏）。
// 其余行一律转发——错误与警告一个都不能吞。
func isAria2SummaryBlockLine(line string) bool {
	s := strings.TrimSpace(strings.TrimRight(line, "\r"))
	switch {
	case s == "":
		return false
	case strings.HasPrefix(s, "*** Download Progress Summary"):
		return true
	case strings.HasPrefix(s, "FILE:"):
		return true
	case s[0] == '[':
		return true
	case strings.Trim(s, "=") == "" && len(s) >= 8:
		return true
	case strings.Trim(s, "-") == "" && len(s) >= 8:
		return true
	}
	return false
}

// aria2ProgressPump 把 aria2c 的 stdout/stderr（挂观察者时两条流都接进来）逐行喂给解析器并
// **等泵退出**（这样调用方返回时，已写出的摘要行一定已经变成事件，用例与收尾都不必等异步）。
type aria2ProgressPump struct {
	pw   *io.PipeWriter
	done chan struct{}
}

// startAria2ProgressPump 启动解析泵，返回要交给 cmd.Stdout/cmd.Stderr 的 writer。
// emit 在泵的协程里被同步调用（回调必须快速返回，见 ProgressObserver 的契约）。
func startAria2ProgressPump(emit func(aria2Summary)) *aria2ProgressPump {
	pr, pw := io.Pipe()
	pump := &aria2ProgressPump{pw: pw, done: make(chan struct{})}
	go func() {
		defer close(pump.done)
		defer pr.Close()
		sc := bufio.NewScanner(pr)
		// aria2 的行都很短（一行摘要 < 200 字节），给 64KiB 起步、1MiB 上限是防畸形输入。
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		downstream := aria2ProgressDownstream()
		// inSummaryBlock：上一条被消费的行属于摘要块。用来把块尾的空行也吃掉——
		// 否则每秒一块会在下游留下每秒一个空行（摘要块被「完整」消费才有意义）。
		inSummaryBlock := false
		for sc.Scan() {
			line := sc.Text()
			if summary, ok := parseAria2SummaryLine(line); ok {
				emit(summary)
				inSummaryBlock = true
				continue
			}
			if isAria2SummaryBlockLine(line) {
				inSummaryBlock = true
				continue
			}
			if inSummaryBlock && strings.TrimSpace(strings.TrimRight(line, "\r")) == "" {
				continue // 摘要块尾部的空行：属于被消费的块
			}
			inSummaryBlock = false
			if downstream != nil {
				// 原样转发（含行内的 \r：aria2 的就地重绘行照旧）
				fmt.Fprintln(downstream, line)
			}
		}
	}()
	return pump
}

// io.Writer / io.Closer 两个方法分开实现，交给 exec.Cmd 的是 writer 那一半。
func (p *aria2ProgressPump) Write(b []byte) (int, error) { return p.pw.Write(b) }

// Close 关闭管道并等泵处理完所有已写出的行。重复调用安全（io.PipeWriter.Close 幂等）。
func (p *aria2ProgressPump) Close() error {
	err := p.pw.Close()
	<-p.done
	return err
}
