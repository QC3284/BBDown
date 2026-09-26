package download

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
	"github.com/QC3284/BBDown/internal/util"
)

// 本文件是**管道契约**的守卫：非 TTY 下 -I 的裸 URL 行、--progress-json 的逐行 JSON
// 必须逐字节保持改版前的形态。
//
// 为什么单独一个文件：美化（灰色截断直链、标题行、列对齐）与「脚本按行取地址」是两套
// 需求，前者只能活在终端里，后者是外部集成的接口。放在同一个文件里，改动时一眼能看到
// 哪些字节是不能动的。

// fakeTerminal 把终端判定钉成给定值（用例结束后还原）。
func fakeTerminal(t *testing.T, isTTY bool) {
	t.Helper()
	restore := util.SetTerminalForTest(func() bool { return isTTY })
	t.Cleanup(restore)
}

// urlFixture 是一条真实形态的 CDN 直链：查询串几百字符，路径里带分片文件名。
func urlFixture(host string) string {
	return "https://" + host + "/upgcxcode/31/21/62131/62131_da3-1-100023.m4s?e=" +
		strings.Repeat("Ab3", 80) + "&deadline=1790429382&upsig=deadbeef"
}

// TestOnlyShowInfoKeepsRawURLInPipe 钉住 -I 在管道里的直链行：**逐字节就是那条 URL**，
// 独占一行、无前缀、无缩进、无截断、无颜色。脚本按行取地址是本仓的对外契约。
//
// 变异验证：把 printStreamURL 的非终端分支改成走内容通道 + ElideURL（把终端那套搬到管道）
// → 「URL 行必须与原文逐字节相等」与「不得出现省略号」两条断言同时变红。
func TestOnlyShowInfoKeepsRawURLInPipe(t *testing.T) {
	fakeTerminal(t, false)

	videoURL := urlFixture("upos-sz-mirrorcos.bilivideo.com")
	audioURL := urlFixture("upos-sz-mirror08h.bilivideo.com")
	result := &entity.ParsedResult{
		VideoTracks: []entity.Video{{Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100, BaseURL: videoURL}},
		AudioTracks: []entity.Audio{{Codecs: "mp4a.40.2", Bandwidth: 132, Dur: 100, BaseURL: audioURL}},
	}
	out := captureStdout(t, func() { PrintAllTracks(result, 100, true) })

	for _, want := range []string{videoURL, audioURL} {
		if !strings.Contains(out, "\n"+want+"\n") {
			t.Errorf("-I 的直链必须是独占一行、无缩进、无补空格的原文：\n%s", out)
		}
		if got := strings.Count(out, want+"\n"); got != 1 {
			t.Errorf("直链 %q 应当恰好出现 1 行（实际 %d）：\n%s", want, got, out)
		}
	}
	// 管道里既不该有颜色，也不该有截断标记。
	if strings.Contains(out, "\x1b[") {
		t.Errorf("非终端输出不该带 ANSI 色码：%q", out)
	}
	if strings.Contains(out, "…") || strings.Contains(out, "↳") {
		t.Errorf("非终端的直链行不该被截断/加标记：%q", out)
	}
}

// TestOnlyShowInfoElidesURLOnTerminal 是上一条的另一半：终端里直链改成灰色截断的提示行
// （几百字符的 CDN 直链会把表格冲散），完整地址改用 --print-urls 取。
//
// 变异验证：去掉 printStreamURL 的终端分支（一律原样打印）→ 「终端下不该出现原始全文」
// 断言红。
func TestOnlyShowInfoElidesURLOnTerminal(t *testing.T) {
	fakeTerminal(t, true)

	videoURL := urlFixture("upos-sz-mirrorcos.bilivideo.com")
	result := &entity.ParsedResult{
		VideoTracks: []entity.Video{{Dfn: "1080P 高清", Res: "1920x1080", Codecs: "AVC", FPS: "30", Bandwidth: 3000, Dur: 100, BaseURL: videoURL}},
	}
	out := captureStdout(t, func() { PrintAllTracks(result, 100, true) })

	if strings.Contains(out, videoURL) {
		t.Errorf("终端下不该把整条 CDN 直链原样插进表格：\n%s", out)
	}
	if !strings.Contains(out, "↳") {
		t.Errorf("终端下的直链提示行缺少标记 ↳：\n%s", out)
	}
	if !strings.Contains(out, "/…/") {
		t.Errorf("终端下的直链应当折叠路径中段：\n%s", out)
	}
	for _, line := range strings.Split(stripANSI(out), "\n") {
		if !strings.Contains(line, "↳") {
			continue
		}
		if w := DisplayWidth(line); w > contentLinkWidth+8 {
			t.Errorf("直链提示行 %d 列，超过上限：%q", w, line)
		}
	}
}

// TestProgressJSONLineContract 钉住 --progress-json 的行级契约：一行一个 JSON 对象、
// 字段名与顺序固定、没有前缀/后缀/颜色（外部 GUI 与监控按行解析）。
//
// 变异验证：给事件行加任何前缀（时间戳/标签）→ 正则不再匹配，本用例红。
func TestProgressJSONLineContract(t *testing.T) {
	var buf bytes.Buffer
	orig := progressJSONOut
	progressJSONOut = func() io.Writer { return &buf }
	t.Cleanup(func() { progressJSONOut = orig })

	// 速度只在满 1 秒时结算，这里两次调用在同一瞬间完成 → speed 通常为 0；正则容忍任意
	// 数值，避免慢 runner 上偶发结算把用例拖红。
	e := newJSONProgressEmitter(1000)
	e.progress(250)
	e.done(1000)

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	wants := []*regexp.Regexp{
		regexp.MustCompile("^\\{\"percent\":25,\"downloaded\":250,\"total\":1000,\"speed\":[0-9.eE+-]+,\"state\":\"progress\"\\}$"),
		regexp.MustCompile("^\\{\"percent\":100,\"downloaded\":1000,\"total\":1000,\"speed\":[0-9.eE+-]+,\"state\":\"done\"\\}$"),
	}
	if len(lines) != len(wants) {
		t.Fatalf("应有 %d 行事件，实际 %d 行：%q", len(wants), len(lines), buf.String())
	}
	for i, want := range wants {
		if !want.MatchString(lines[i]) {
			t.Errorf("第 %d 行不符合逐行 JSON 契约：\n得到 %q\n期望匹配 %s", i+1, lines[i], want)
		}
	}
}

// TestRenderInfoJSONWireContract 钉住 --info-json 的**字节**契约：字段顺序、缩进、末尾换行
// 一字不动（脚本/GUI 直接吃这段输出；改字段或改缩进都属破坏性变更，必须在同一次改动里
// 更新这份期望并说明理由）。
//
// 变异验证：RenderInfoJSON 换成 json.Marshal（去掉缩进）→ 逐字节比对红。
func TestRenderInfoJSONWireContract(t *testing.T) {
	out, err := RenderInfoJSON(InfoPayload{
		Title: "标题", Bvid: "BV1xx411c7mD", Aid: "2", Cid: "62131", PageIndex: 1, PageTitle: "P1", DurationSec: 2055,
		Video: []entity.Video{{ID: "32", Dfn: "480P 清晰", Res: "512x384", FPS: "15.009", Codecs: "AVC", Bandwidth: 206, Dur: 2055, BaseURL: "https://cdn/v.m4s"}},
		Audio: []entity.Audio{{ID: "30280", Codecs: "mp4a.40.2", Bandwidth: 134, Dur: 2055, BaseURL: "https://cdn/a.m4s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "title": "标题",
  "bvid": "BV1xx411c7mD",
  "aid": "2",
  "cid": "62131",
  "page_index": 1,
  "page_title": "P1",
  "duration_sec": 2055,
  "video": [
    {
      "id": "32",
      "dfn": "480P 清晰",
      "base_url": "https://cdn/v.m4s",
      "res": "512x384",
      "fps": "15.009",
      "codecs": "AVC",
      "bandwidth": 206,
      "dur": 2055,
      "size": 0
    }
  ],
  "audio": [
    {
      "id": "30280",
      "dfn": "",
      "base_url": "https://cdn/a.m4s",
      "codecs": "mp4a.40.2",
      "bandwidth": 134,
      "dur": 2055
    }
  ]
}
`
	if out != want {
		t.Errorf("--info-json 的输出必须逐字节稳定：\n得到 %q\n期望 %q", out, want)
	}
}
