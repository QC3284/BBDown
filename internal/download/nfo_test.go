package download

import (
	"strings"
	"testing"
)

// NFO 侧车元数据：字段齐全、XML 转义正确、单P 不写 episode、时间戳为 0 时不写 aired。
//
// 变异验证：删掉 UniqueID 那条 → 「必须带 bilibili uniqueid」断言变红。
func TestRenderNFO(t *testing.T) {
	out, err := RenderNFO("标题 & 副标题", "P3 分P", "某UP主", "BV1xx411c7mD", 3, 1698553200)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>",
		"<movie>",
		"<title>P3 分P</title>",
		"<plot>标题 &amp; 副标题</plot>",
		"<studio>某UP主</studio>",
		"<aired>2023-10-29</aired>",
		"<uniqueid type=\"bilibili\" default=\"true\">BV1xx411c7mD</uniqueid>",
		"<episode>3</episode>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("NFO 缺少 %q\n实际:\n%s", want, out)
		}
	}

	// 单P：不写 episode；不传分P标题时用稿件标题；时间未知不写 aired。
	plain, err := RenderNFO("主标题", "", "UP", "BV1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "episode") {
		t.Errorf("单P 不该写 episode：%s", plain)
	}
	if strings.Contains(plain, "aired") {
		t.Errorf("时间未知不该写 aired：%s", plain)
	}
	if !strings.Contains(plain, "<title>主标题</title>") {
		t.Errorf("单P 应当用稿件标题：%s", plain)
	}
}
