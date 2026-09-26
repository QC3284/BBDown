package workflow

import (
	"fmt"
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
// 排序返回副本，不改动入参。
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
