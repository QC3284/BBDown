package workflow

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// 播放列表（M3U）侧车：把同一个稿件本次产出的文件串成一条可播放的列表——多P不必再去
// 文件管理器里手工挑文件、手工排序，VLC / mpv / PotPlayer 直接打开。
//
// 与 NFO 侧车同一纪律：渲染层是纯函数（可离线表驱动），写入层只告警——播放列表是附加价值，
// 它出问题绝不能牵连已经下载好的产物。

// M3UEntry 是播放列表的一条：标题、相对播放列表所在目录的路径、时长（秒，<=0 表示未知）。
// Index 是分P序号，只用于排序、不进 M3U 文本：它让「按分P顺序」由数据决定，而不是依赖调用顺序。
type M3UEntry struct {
	Title    string
	Path     string
	Duration int
	Index    int
}

// m3uUnknownDuration 是 M3U 约定的「时长未知」（-1）：播放器据此显示 --:--，
// 而不是把 0 当成零长度。
const m3uUnknownDuration = -1

// RenderM3U 渲染播放列表全文（UTF-8，含 #EXTM3U 头），按给定顺序逐条输出。
//
// 每条两行：#EXTINF:<秒>,<标题> 与紧随其后的路径；时长未知写 -1。
//   - 标题里的换行会切碎行结构（多出来的行会被当成路径），折叠成空格；
//   - 标题里的逗号原样保留——M3U 的标题定义为「首个逗号之后的全部内容」，按首个逗号切分
//     即可原样读回（m3u_test.go 有回读断言），加反斜杠转义反而会让播放器把反斜杠也显示出来；
//   - 路径里的换行无法表达，只能跳过该条：写出去只会得到一个指向错误文件的列表。
func RenderM3U(entries []M3UEntry) string {
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	for _, e := range entries {
		if e.Path == "" || strings.ContainsAny(e.Path, "\r\n") {
			continue
		}
		dur := e.Duration
		if dur <= 0 {
			dur = m3uUnknownDuration
		}
		sb.WriteString("#EXTINF:")
		sb.WriteString(strconv.Itoa(dur))
		sb.WriteByte(',')
		sb.WriteString(sanitizeM3UTitle(e.Title))
		sb.WriteByte('\n')
		sb.WriteString(e.Path)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// sanitizeM3UTitle 把标题压成一行：CR/LF 折叠为空格（行结构比标题里的换行重要），
// 其余字符原样保留（逗号、#、中文都不需要转义）。
func sanitizeM3UTitle(title string) string {
	if !strings.ContainsAny(title, "\r\n") {
		return title
	}
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")
	return strings.TrimSpace(r.Replace(title))
}

// AppendM3UEntry 追加一条产物：路径已经出现过就原样返回（不重复、保持首次出现的顺序）。
//
// 重复是真实存在的——自定义模板不含 <pageNumber> 时多个分P会落到同一个产物文件；
// 重跑时被「已存在, 跳过下载」的分P也会再登记一次。
func AppendM3UEntry(entries []M3UEntry, e M3UEntry) []M3UEntry {
	for _, old := range entries {
		if old.Path == e.Path {
			return entries
		}
	}
	return append(entries, e)
}

// sortM3UEntries 按分P序号稳定排序（序号相同保持首次出现的顺序），返回副本、不动入参。
//
// 「按分P顺序」不能依赖「Run 恰好是按 pagesInfo 顺序下载」这个调用顺序的巧合：
// 顺序一旦错，用户看到的是一条乱序的播放列表，而没人会去核对它。
func sortM3UEntries(entries []M3UEntry) []M3UEntry {
	out := append([]M3UEntry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// m3uFileName 取播放列表文件名：用稿件标题（与产物目录同源）；标题全是非法字符时退回固定名，
// 避免写出 ".m3u" 这种隐藏文件。
func m3uFileName(title string) string {
	if name := util.SanitizePathSegment(strings.TrimSpace(title)); name != "" {
		return name + ".m3u"
	}
	return "playlist.m3u"
}

// m3uPlaylist 累积一个稿件的产物（Path 是相对 dir 的路径），每登记一条就整体重写文件：
// 一个稿件的分P数有限，重写换来的是「任何时刻文件里都是当前完整的列表」。
type m3uPlaylist struct {
	path    string
	dir     string
	entries []M3UEntry
}

// writeM3USidecar 在产物目录登记一条产物并重写 .m3u（--write-m3u 未开、或产物不存在时什么都不做）。
//
// 播放列表按稿件聚合，所以状态挂在 Workflow 上：每成功产出一个分P就登记并重写一次，
// 运行结束时文件里是本次运行的全部产物、按分P顺序。
// 任何写入失败都只告警：列表写不出来不影响下载结果（与 writeNFOSidecar 同规矩）。
func (w *Workflow) writeM3USidecar(savePath, title string, page entity.Page) {
	if !w.Cfg.WriteM3U || savePath == "" {
		return
	}
	if _, err := os.Stat(savePath); err != nil {
		return // 没有产物就没有可播放的东西（--skip-mux / 只下字幕 / -I 都不产出 savePath）
	}
	if w.m3u == nil {
		dir := filepath.Dir(savePath)
		w.m3u = &m3uPlaylist{path: filepath.Join(dir, m3uFileName(title)), dir: dir}
	}
	rel, err := filepath.Rel(w.m3u.dir, savePath)
	if err != nil {
		rel = savePath // 跨盘符等算不出相对路径：退回原路径，宁可写得难看也不要指错文件
	}
	entryTitle := page.Title
	if entryTitle == "" {
		entryTitle = title
	}
	// 时长写「未知」：分P时长是 B 站声明的，试看片段、分段合并后的产物并不等于它；
	// 声明错了播放器会按错误时长定位，-1 让它直接读文件本身。
	w.m3u.entries = AppendM3UEntry(w.m3u.entries,
		M3UEntry{Title: entryTitle, Path: filepath.ToSlash(rel), Duration: m3uUnknownDuration, Index: page.Index})
	if err := os.WriteFile(w.m3u.path, []byte(RenderM3U(sortM3UEntries(w.m3u.entries))), 0o644); err != nil {
		util.LogWarn("写入播放列表失败: %v", err)
		return
	}
	util.LogDebug("已写入播放列表: %s", w.m3u.path)
}
