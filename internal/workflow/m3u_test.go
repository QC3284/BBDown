package workflow

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// M3U 渲染是纯函数：表驱动钉住「空列表 / 单条 / 多条顺序 / 标题里的逗号与换行 / 路径异常」。
//
// 变异验证（每条都要能编译、失败必须是断言而不是构建错误）：
//   - 去掉 #EXTM3U 头 → 「空列表只有头部」「单条」两例红；
//   - 去掉 sanitizeM3UTitle 的换行折叠 → 「标题含换行」红（渲染出多余的行）；
//   - 去掉路径含换行的跳过 → 「空路径与含换行的路径被跳过」红；
//   - 去掉 Duration<=0 → -1 的兜底 → 「时长」红。
func TestRenderM3U(t *testing.T) {
	cases := []struct {
		name    string
		entries []M3UEntry
		want    string
	}{
		{
			name: "空列表只有头部",
			want: "#EXTM3U\n",
		},
		{
			name:    "单条：EXTINF 加相对路径",
			entries: []M3UEntry{{Title: "P1 标题", Path: "[P01]a.mp4", Duration: -1}},
			want:    "#EXTM3U\n#EXTINF:-1,P1 标题\n[P01]a.mp4\n",
		},
		{
			name: "多条按给定顺序",
			entries: []M3UEntry{
				{Title: "第一话", Path: "[P01]a.mp4"},
				{Title: "第二话", Path: "[P02]b.mp4"},
				{Title: "第三话", Path: "[P03]c.mp4"},
			},
			want: "#EXTM3U\n" +
				"#EXTINF:-1,第一话\n[P01]a.mp4\n" +
				"#EXTINF:-1,第二话\n[P02]b.mp4\n" +
				"#EXTINF:-1,第三话\n[P03]c.mp4\n",
		},
		{
			name:    "标题含逗号：首个逗号之后原样保留",
			entries: []M3UEntry{{Title: "标题,带逗号", Path: "a.mp4"}},
			want:    "#EXTM3U\n#EXTINF:-1,标题,带逗号\na.mp4\n",
		},
		{
			name:    "标题含换行：折叠成空格，不产生额外行",
			entries: []M3UEntry{{Title: "上\n下\r\n行", Path: "a.mp4"}},
			want:    "#EXTM3U\n#EXTINF:-1,上 下 行\na.mp4\n",
		},
		{
			name: "时长：未知写 -1，已知写秒",
			entries: []M3UEntry{
				{Title: "未知", Path: "a.mp4", Duration: 0},
				{Title: "已知", Path: "b.mp4", Duration: 300},
			},
			want: "#EXTM3U\n#EXTINF:-1,未知\na.mp4\n#EXTINF:300,已知\nb.mp4\n",
		},
		{
			name: "空路径与含换行的路径被跳过",
			entries: []M3UEntry{
				{Title: "正常", Path: "a.mp4"},
				{Title: "空路径", Path: ""},
				{Title: "换行路径", Path: "b\nc.mp4"},
				{Title: "正常2", Path: "d.mp4"},
			},
			want: "#EXTM3U\n#EXTINF:-1,正常\na.mp4\n#EXTINF:-1,正常2\nd.mp4\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderM3U(c.entries); got != c.want {
				t.Errorf("RenderM3U() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRenderM3UPageComment：分P序号写成自研注释行（#EXT-BBDOWN-PAGE:N）写在每条目前，
// EXTINF 与路径行本身不变；序号未知（0，旧文件回读来的条目）不编造 N、不写注释。
func TestRenderM3UPageComment(t *testing.T) {
	numbered := RenderM3U([]M3UEntry{
		{Title: "第一话", Path: "a.mp4", Index: 1},
		{Title: "第二话", Path: "b.mp4", Index: 2},
	})
	want := "#EXTM3U\n" +
		"#EXT-BBDOWN-PAGE:1\n#EXTINF:-1,第一话\na.mp4\n" +
		"#EXT-BBDOWN-PAGE:2\n#EXTINF:-1,第二话\nb.mp4\n"
	if numbered != want {
		t.Errorf("带序号的渲染 = %q, want %q", numbered, want)
	}

	// 序号未知：不写注释（不能给旧条目编造一个 N）。
	if got := RenderM3U([]M3UEntry{{Title: "旧", Path: "old.mp4"}}); got != "#EXTM3U\n#EXTINF:-1,旧\nold.mp4\n" {
		t.Errorf("序号未知时不该写序号注释：%q", got)
	}
}

// TestRenderM3UPageCommentKeepsPlayerLinesIntact：序号注释只**新增一行注释**——
// 去掉那些注释行之后必须与「不带序号」的渲染逐字相同，播放器看到的 #EXTINF 与路径行
// 因此与 2.15.0 完全一致（这是「不污染播放器解析路径」的可验证形式）。
func TestRenderM3UPageCommentKeepsPlayerLinesIntact(t *testing.T) {
	entries := []M3UEntry{
		{Title: "第一话", Path: "a.mp4", Duration: 300, Index: 1},
		{Title: "标题,带逗号", Path: "b.mp4", Index: 5},
	}
	numbered := RenderM3U(entries)
	plain := make([]M3UEntry, len(entries))
	for i, e := range entries {
		e.Index = 0
		plain[i] = e
	}
	var stripped []string
	for _, l := range strings.Split(strings.TrimSuffix(numbered, "\n"), "\n") {
		if strings.HasPrefix(l, m3uPageComment) {
			continue
		}
		stripped = append(stripped, l)
	}
	if got, want := strings.Join(stripped, "\n")+"\n", RenderM3U(plain); got != want {
		t.Errorf("注释之外的播放器可见行被改动了：\n got %q\nwant %q", got, want)
	}
	if n := strings.Count(numbered, m3uPageComment); n != len(entries) {
		t.Errorf("每条目应当恰好一行序号注释，实际 %d 行：%q", n, numbered)
	}
}

// TestRenderM3UTitleRoundTrip 把「标题里的逗号安全处理」变成可验证的性质：播放器按首个逗号
// 切分 #EXTINF，切出来的标题必须与写入时逐字相同——转义成反斜杠-逗号会把反斜杠一起显示，丢逗号则截断标题。
func TestRenderM3UTitleRoundTrip(t *testing.T) {
	titles := []string{"普通标题", "标题,带逗号", "a,b,c", "逗号收尾,", "带#井号,与逗号"}
	entries := make([]M3UEntry, 0, len(titles))
	for i, s := range titles {
		entries = append(entries, M3UEntry{Title: s, Path: fmt.Sprintf("p%d.mp4", i+1)})
	}
	body := RenderM3U(entries)
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != 1+2*len(titles) {
		t.Fatalf("行数 = %d, want %d（头部 + 每条两行）：%q", len(lines), 1+2*len(titles), body)
	}
	for i, want := range titles {
		extinf := lines[1+2*i]
		idx := strings.Index(extinf, ",")
		if idx < 0 {
			t.Fatalf("第 %d 条不是合法 #EXTINF：%q", i+1, extinf)
		}
		if got := extinf[idx+1:]; got != want {
			t.Errorf("第 %d 条标题 = %q, want %q", i+1, got, want)
		}
	}
}

// TestAppendM3UEntryDedupesByPath 钉住去重语义：按路径去重、保持首次出现的顺序、
// 重复登记不覆盖先到的标题。
//
// 变异验证：去掉 AppendM3UEntry 里的路径比较 → 本用例红（长度、顺序与计数都不符）。
func TestAppendM3UEntryDedupesByPath(t *testing.T) {
	var got []M3UEntry
	got = AppendM3UEntry(got, M3UEntry{Title: "P1", Path: "p1.mp4", Index: 1})
	got = AppendM3UEntry(got, M3UEntry{Title: "P2", Path: "p2.mp4", Index: 2})
	got = AppendM3UEntry(got, M3UEntry{Title: "重复路径", Path: "p1.mp4", Index: 9})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2（同一路径只保留一条）：%+v", len(got), got)
	}
	if got[0].Path != "p1.mp4" || got[1].Path != "p2.mp4" {
		t.Errorf("顺序应为首次出现的顺序，got %q, %q", got[0].Path, got[1].Path)
	}
	if got[0].Title != "P1" {
		t.Errorf("重复登记不该覆盖先到的标题：%q", got[0].Title)
	}
	if n := strings.Count(RenderM3U(got), "p1.mp4\n"); n != 1 {
		t.Errorf("渲染结果里 p1.mp4 出现 %d 次，want 1", n)
	}
}

// TestSortM3UEntriesByPageIndex 钉住「按分P顺序」由分P序号决定，而不是依赖调用顺序；
// 排序返回副本，不改动入参；序号未知（旧格式列表）的条目按 2.15.0 语义视为最小、保持出现序。
//
// 变异验证：把 sortM3UEntries 改成直接返回入参 → 本用例红（顺序仍是 3,1,2）；
// 去掉复制（原地 sort）→ 入参断言红。
func TestSortM3UEntriesByPageIndex(t *testing.T) {
	in := []M3UEntry{
		{Title: "P3", Path: "c.mp4", Index: 3},
		{Title: "P1", Path: "a.mp4", Index: 1},
		{Title: "P2", Path: "b.mp4", Index: 2},
	}
	got := sortM3UEntries(in)
	want := []string{"a.mp4", "b.mp4", "c.mp4"}
	for i, w := range want {
		if got[i].Path != w {
			t.Fatalf("排序后第 %d 条 = %q, want %q（完整：%+v）", i, got[i].Path, w, got)
		}
	}
	if in[0].Path != "c.mp4" {
		t.Errorf("排序不该改动入参：%+v", in)
	}

	// 序号未知（0：没有 #EXT-BBDOWN-PAGE 注释的旧 M3U 回读条目）视为最小：它们保持出现序、
	// 排在最前面——就是 2.15.0 那一行比较的语义，不再额外按「有没有序号」分组。
	mixed := []M3UEntry{
		{Path: "old1.mp4"},
		{Path: "p2.mp4", Index: 2},
		{Path: "old2.mp4"},
		{Path: "p1.mp4", Index: 1},
	}
	gotMixed := sortM3UEntries(mixed)
	for i, w := range []string{"old1.mp4", "old2.mp4", "p1.mp4", "p2.mp4"} {
		if gotMixed[i].Path != w {
			t.Fatalf("混合排序第 %d 条 = %q, want %q（完整：%+v）", i, gotMixed[i].Path, w, gotMixed)
		}
	}

	// 升级混合场景（旧格式列表 + 本次新下的 P4）：旧列表的顺序不能被改写，结果仍是 P01..P04。
	// 这是「未知=最小」而非「有序号在前」的原因——后者会把 P4 提到旧列表前面。
	upgrade := []M3UEntry{
		{Title: "第一话", Path: "[P01]第一话.mp4"},
		{Title: "第二话", Path: "[P02]第二话.mp4"},
		{Title: "第三话", Path: "[P03]第三话.mp4"},
		{Title: "第四话", Path: "[P04]第四话.mp4", Index: 4},
	}
	gotUpgrade := sortM3UEntries(upgrade)
	for i, w := range []string{"[P01]第一话.mp4", "[P02]第二话.mp4", "[P03]第三话.mp4", "[P04]第四话.mp4"} {
		if gotUpgrade[i].Path != w {
			t.Fatalf("升级混合排序第 %d 条 = %q, want %q（完整：%+v）", i, gotUpgrade[i].Path, w, gotUpgrade)
		}
	}
}

// TestM3UFileName 播放列表文件名：标题里的路径分隔符会被净化（不能让服务端标题决定写到哪），
// 标题全是非法字符时退回固定名，不写 ".m3u" 这种隐藏文件。
func TestM3UFileName(t *testing.T) {
	cases := []struct{ title, want string }{
		{"剧", "剧.m3u"},
		{"a/b", "a_b.m3u"},
		{"..", "_.m3u"},
		{"", "playlist.m3u"},
		{"   ", "playlist.m3u"},
	}
	for _, c := range cases {
		if got := m3uFileName(c.title); got != c.want {
			t.Errorf("m3uFileName(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

// TestParseM3U 回读纯函数：第一条非空行必须是 #EXTM3U（完整性判据，缺了算损坏）；
// #EXTINF 的标题与时长要跟着路径读回来；空行与其它 # 指令/注释行都不是条目；
// 路径行不做切分（路径里的逗号原样保留）。
//
// 变异验证：
//   - 去掉头部校验 → 「空文件」「缺少 #EXTM3U 头」两例的 ok 断言红；
//   - 去掉 #EXTINF 解析 → 「EXTINF 的标题与时长跟着路径读回」例红（标题与时长丢了）。
func TestParseM3U(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []M3UEntry
		ok   bool
	}{
		{name: "只有头部：合法但没有条目", body: "#EXTM3U\n", ok: true},
		{
			name: "EXTINF 的标题与时长跟着路径读回",
			body: "#EXTM3U\n#EXTINF:300,标题,带逗号\n[P01]a.mp4\n#EXTINF:-1,\nb.mp4\n",
			want: []M3UEntry{
				{Title: "标题,带逗号", Path: "[P01]a.mp4", Duration: 300},
				{Path: "b.mp4", Duration: -1},
			},
			ok: true,
		},
		{
			name: "CRLF、空行与其它指令行都不是条目，路径里的逗号原样保留",
			body: "#EXTM3U\r\n\r\n#EXTGRP:组\r\n#EXTINF:100,x\r\na,b.mp4\r\n#EXTVLCOPT:network-caching=1000\r\nc.mp4\r\n",
			want: []M3UEntry{
				{Title: "x", Path: "a,b.mp4", Duration: 100},
				{Path: "c.mp4"},
			},
			ok: true,
		},
		{
			name: "UTF-8 BOM 不当作损坏",
			body: "\ufeff#EXTM3U\na.mp4\n",
			want: []M3UEntry{{Path: "a.mp4"}},
			ok:   true,
		},
		{
			name: "非法时长按未知（0）处理",
			body: "#EXTM3U\n#EXTINF:abc,x\na.mp4\n",
			want: []M3UEntry{{Title: "x", Path: "a.mp4"}},
			ok:   true,
		},
		{
			name: "分P序号注释跟着路径读回",
			body: "#EXTM3U\n#EXT-BBDOWN-PAGE:1\n#EXTINF:300,第一话\na.mp4\n#EXT-BBDOWN-PAGE:5\n#EXTINF:500,第五话\ne.mp4\n",
			want: []M3UEntry{
				{Title: "第一话", Path: "a.mp4", Duration: 300, Index: 1},
				{Title: "第五话", Path: "e.mp4", Duration: 500, Index: 5},
			},
			ok: true,
		},
		{
			name: "序号非法或非正时按未知（0）处理，不算损坏",
			body: "#EXTM3U\n#EXT-BBDOWN-PAGE:abc\n#EXTINF:1,x\na.mp4\n#EXT-BBDOWN-PAGE:0\n#EXTINF:1,y\nb.mp4\n",
			want: []M3UEntry{
				{Title: "x", Path: "a.mp4", Duration: 1},
				{Title: "y", Path: "b.mp4", Duration: 1},
			},
			ok: true,
		},
		{
			name: "旧 M3U（没有序号注释）回读结果与 2.15.0 相同：Index 恒 0",
			body: "#EXTM3U\n#EXTINF:100,旧一\na.mp4\n#EXTINF:-1,旧二\nb.mp4\n",
			want: []M3UEntry{
				{Title: "旧一", Path: "a.mp4", Duration: 100},
				{Title: "旧二", Path: "b.mp4", Duration: -1},
			},
			ok: true,
		},
		{name: "空文件视为损坏", body: "", ok: false},
		{name: "缺少 #EXTM3U 头视为损坏", body: "a.mp4\nb.mp4\n", ok: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseM3U(c.body)
			if ok != c.ok {
				t.Fatalf("parseM3U(%q) ok = %v, want %v", c.body, ok, c.ok)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseM3U(%q) = %+v, want %+v", c.body, got, c.want)
			}
		})
	}
}

// TestM3UReadBackRoundTrip 回读必须是渲染的逆：渲染 → 回读 → 再渲染逐字相同。
// 否则每次重跑合并都会悄悄改写旧条目——丢标题、把已经能显示的时长打回 -1。
func TestM3UReadBackRoundTrip(t *testing.T) {
	entries := []M3UEntry{
		{Title: "第一话", Path: "[P01]第一话.mp4", Duration: 300},
		{Title: "标题,带逗号", Path: "b.mp4"},
		{Title: "", Path: "c.mp4", Duration: -1},
	}
	first := RenderM3U(entries)
	back, ok := parseM3U(first)
	if !ok {
		t.Fatalf("自己渲染出来的列表必须能被读回：%q", first)
	}
	if second := RenderM3U(back); second != first {
		t.Errorf("回读再渲染应逐字相同：\n got %q\nwant %q", second, first)
	}

	// 带序号的条目同样要能回环：序号（注释行）也必须原样回来，
	// 否则「读回 → 合并 → 重写」会把已经记下的分P序号洗掉，乱序分批修复随之失效。
	numbered := []M3UEntry{
		{Title: "第五话", Path: "e.mp4", Duration: 500, Index: 5},
		{Title: "第一话", Path: "a.mp4", Duration: 300, Index: 1},
	}
	firstNum := RenderM3U(numbered)
	backNum, ok := parseM3U(firstNum)
	if !ok {
		t.Fatalf("带序号的列表必须能被读回：%q", firstNum)
	}
	for i, want := range []int{5, 1} {
		if backNum[i].Index != want {
			t.Errorf("第 %d 条序号 = %d, want %d", i+1, backNum[i].Index, want)
		}
	}
	if second := RenderM3U(backNum); second != firstNum {
		t.Errorf("带序号回读再渲染应逐字相同：\n got %q\nwant %q", second, firstNum)
	}
}
