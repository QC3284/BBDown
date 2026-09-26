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

// 播放列表（M3U）侧车：把同一个稿件已产出的文件（含之前运行下好的分P）串成一条可播放的
// 列表——多P不必再去文件管理器里手工挑文件、手工排序，VLC / mpv / PotPlayer 直接打开。
// 本次运行只登记自己产出的分P，写入前读回已有列表并合并：`-p 1` 重跑不会把上一次下好的
// P2..PN 从列表里抹掉。
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
	if m3uEntryIndex(entries, e.Path) >= 0 {
		return entries
	}
	return append(entries, e)
}

// m3uEntryIndex 返回路径在列表中的下标；不存在返回 -1。
func m3uEntryIndex(entries []M3UEntry, path string) int {
	for i := range entries {
		if entries[i].Path == path {
			return i
		}
	}
	return -1
}

// parseM3UExtinf 从 "#EXTINF:<秒>,<标题>" 里取标题与秒数；秒数缺失或非法记 0（渲染时回落 -1）。
// 标题按首个逗号切分——写出去的标题从不在逗号前加转义，所以首个逗号之后就是原样的标题。
func parseM3UExtinf(line string) (title string, duration int) {
	spec := strings.TrimPrefix(line, "#EXTINF:")
	i := strings.Index(spec, ",")
	if i < 0 {
		return "", 0
	}
	if d, err := strconv.Atoi(strings.TrimSpace(spec[:i])); err == nil {
		duration = d
	}
	return spec[i+1:], duration
}

// parseM3U 回读已有播放列表（供合并）：路径行是条目，#EXTINF 是紧随其后那条的元数据。
//
//   - 第一条非空行必须是 #EXTM3U，否则判为损坏（ok=false）：不能把任意文本当成路径并进播放列表；
//   - 其余以 # 开头的行是注释/指令（#EXTGRP、#EXTVLCOPT…），与空行一并跳过；
//   - 回读的条目没有分P序号（M3U 文本里没有这个字段），Index 记 0：稳定排序会保持它们在
//     文件里的原顺序，而文件本来就是我们按分P顺序写的。
//
// 保留 #EXTINF 的标题与时长是必要的：只认路径行会让回读的每条都退化成「无标题、时长未知」，
// 而合并结果马上覆盖旧文件——一次重跑就把旧条目已经能显示的标题与长度抹掉。
func parseM3U(body string) (entries []M3UEntry, ok bool) {
	var (
		title    string
		duration int
		sawHead  bool
	)
	for _, raw := range strings.Split(body, "\n") {
		// 记事本另存会加 UTF-8 BOM：剥掉行首的那个，别让它把整个老列表判成损坏。
		line := strings.TrimPrefix(strings.TrimRight(raw, "\r"), "\ufeff")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !sawHead {
			if strings.TrimSpace(line) != "#EXTM3U" {
				return nil, false
			}
			sawHead = true
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			title, duration = parseM3UExtinf(line)
		case strings.HasPrefix(line, "#"):
			// 其它指令/注释行：不是条目。
		default:
			entries = append(entries, M3UEntry{Title: title, Path: line, Duration: duration})
			title, duration = "", 0
		}
	}
	return entries, sawHead
}

// readM3UEntries 读回已有播放列表用于合并；任何问题都只让列表退化成空，绝不中断下载：
// 文件不存在 = 首次运行（静默），读取失败或内容损坏（缺 #EXTM3U 头）= 当空 + debug 一行。
func readM3UEntries(path string) []M3UEntry {
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			util.LogDebug("读取播放列表失败，按空列表合并: %s: %v", path, err)
		}
		return nil
	}
	entries, ok := parseM3U(string(body))
	if !ok {
		util.LogDebug("播放列表损坏（缺少 #EXTM3U 头），按空列表合并: %s", path)
		return nil
	}
	return entries
}

// mergeM3UEntries 把回读的旧条目与本次登记的条目合并成一条列表：按路径去重，按分P序号稳定排序。
//
//   - 旧条目沿用它在文件里的位置（回读条目 Index=0，稳定排序保持原顺序），重复路径只留一条；
//   - 路径已在旧列表里时用本次的标题/时长刷新它：2.10.0 之前写出的列表时长一律是 -1，
//     不刷新的话重跑/补下永远修不好这些条目，播放器也就一直显示不出长度；
//   - 本次新登记的路径追加到末尾（同样按路径去重，同一次运行内首次登记为准）。
func mergeM3UEntries(existing, current []M3UEntry) []M3UEntry {
	var (
		out    []M3UEntry
		oldPos = make(map[string]int, len(existing))
		seen   = make(map[string]bool, len(existing))
	)
	for _, e := range existing {
		if seen[e.Path] {
			continue // 旧文件里也可能有重复路径（手工编辑、历史写入），同样只留一条
		}
		seen[e.Path] = true
		oldPos[e.Path] = len(out)
		out = append(out, e)
	}
	for _, e := range current {
		if i, ok := oldPos[e.Path]; ok {
			if e.Title != "" {
				out[i].Title = e.Title
			}
			if e.Duration > 0 {
				out[i].Duration = e.Duration
			}
			continue
		}
		out = AppendM3UEntry(out, e)
	}
	return sortM3UEntries(out)
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
//
// 分两层：existing 是本次运行开始时从文件读回的旧条目（Index 未知），entries 是本次运行
// 登记的条目。写入时两者合并——只写 entries 的话，`-p 1` 重跑会把上一次下好的 P2..PN
// 从列表里抹掉（文件都还在，列表却指不全）。
type m3uPlaylist struct {
	path     string
	dir      string
	existing []M3UEntry
	entries  []M3UEntry
}

// writeM3USidecar 在产物目录登记一条产物并重写 .m3u（--write-m3u 未开、或产物不存在时什么都不做）。
//
// 播放列表按稿件聚合，所以状态挂在 Workflow 上：每成功产出一个分P就登记并重写一次，
// 运行结束时文件里是「本次运行的产物 + 读回的已有条目」、按分P顺序。
// 任何读写失败都只告警：列表读不出/写不出不影响下载结果（与 writeNFOSidecar 同规矩）。
func (w *Workflow) writeM3USidecar(savePath, title string, page entity.Page) {
	if !w.Cfg.WriteM3U || savePath == "" {
		return
	}
	if _, err := os.Stat(savePath); err != nil {
		return // 没有产物就没有可播放的东西（--skip-mux / 只下字幕 / -I 都不产出 savePath）
	}
	if w.m3u == nil {
		dir := filepath.Dir(savePath)
		listPath := filepath.Join(dir, m3uFileName(title))
		w.m3u = &m3uPlaylist{
			path: listPath,
			dir:  dir,
			// 读回已有列表并合并，而不是从头覆写：`-p 1` 只登记 P1，覆写会把上次下好的
			// P2..PN 从列表里抹掉。读不出来（不存在/损坏）当空列表。
			existing: readM3UEntries(listPath),
		}
	}
	rel, err := filepath.Rel(w.m3u.dir, savePath)
	if err != nil {
		rel = savePath // 跨盘符等算不出相对路径：退回原路径，宁可写得难看也不要指错文件
	}
	entryTitle := page.Title
	if entryTitle == "" {
		entryTitle = title
	}
	// 时长用分P声明的秒数（page.Dur）：播放器据此显示长度、进度条知道总长；
	// 接口没给时长（<=0）时渲染层回落 -1，播放器显示 --:-- 并直接读文件本身。
	// 已知局限：试看片段/裁剪后的产物与声明时长可能对不上，但显示长度仍比显示 --:-- 有用。
	w.m3u.entries = AppendM3UEntry(w.m3u.entries,
		M3UEntry{Title: entryTitle, Path: filepath.ToSlash(rel), Duration: page.Dur, Index: page.Index})
	if err := os.WriteFile(w.m3u.path, []byte(RenderM3U(mergeM3UEntries(w.m3u.existing, w.m3u.entries))), 0o644); err != nil {
		util.LogWarn("写入播放列表失败: %v", err)
		return
	}
	util.LogDebug("已写入播放列表: %s", w.m3u.path)
}
