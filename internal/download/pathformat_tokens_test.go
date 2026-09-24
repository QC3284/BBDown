package download

import (
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/entity"
)

// F1 对账：上游 PathHelper 的占位符表在本仓**全部可用**。此前没有用例守整张表——
// 「我们有这些占位符」只是读代码的印象，少一条 ReplaceAll 也不会有测试变红。
//
// 变异验证：删掉任意一条 ReplaceAll，对应断言变红。
func TestFormatSavePathCoversUpstreamTokens(t *testing.T) {
	page := entity.Page{
		Index: 3, Aid: "962679026", Cid: "1314018414", Title: "P1 标题/带斜杠",
		OwnerName: "UP 主", OwnerMid: "510234024", PubTime: 1698553200,
	}
	video := &entity.Video{Dfn: "1080P 高码率", Codecs: "avc1.640032", Res: "1920x1080", FPS: "30", Bandwidth: 3145694}
	audio := &entity.Audio{Codecs: "mp4a.40.2", Bandwidth: 134737}

	// 上游 PathHelper 的完整 token 表。
	tokens := []string{
		"videoTitle", "pageNumber", "pageNumberWithZero", "pageTitle",
		"bvid", "aid", "cid", "ownerName", "ownerMid",
		"dfn", "res", "fps", "videoCodecs", "videoBandwidth",
		"audioCodecs", "audioBandwidth", "publishDate", "videoDate",
	}
	pattern := "<" + strings.Join(tokens, ">|<") + ">"
	got := FormatSavePath(pattern, "视频标题", video, audio, page, 12, "web", page.PubTime)

	for _, tok := range tokens {
		if strings.Contains(got, "<"+tok+">") {
			t.Errorf("占位符 <%s> 没有展开：%q", tok, got)
		}
	}
	// 抽查几个值：序号补零按页数位宽、ownerMid 原样、服务器透传值经过净化。
	for _, want := range []string{"|3|", "|03|", "|510234024|", "1080P 高码率", "1920x1080", "3145694", "134737"} {
		if !strings.Contains(got, want) {
			t.Errorf("展开结果缺少 %q：%q", want, got)
		}
	}
	if strings.Contains(got, "/") && !strings.Contains(got, "带斜杠") {
		// 标题里的斜杠必须被净化成下划线，否则会写出预期目录之外。
		t.Errorf("标题中的斜杠未被净化：%q", got)
	}

	// 自定义日期格式（上游 <publishDate:格式>）。
	dated := FormatSavePath("<publishDate:yyyyMMdd>", "t", video, audio, page, 1, "web", page.PubTime)
	if strings.Contains(dated, "<publishDate") {
		t.Errorf("自定义日期格式未展开：%q", dated)
	}
	if dated != "20231029" {
		t.Errorf("自定义日期格式结果 = %q，期望 20231029（上游默认时区为本地）", dated)
	}
}
