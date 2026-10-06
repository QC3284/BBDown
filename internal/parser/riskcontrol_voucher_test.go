package parser

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QC3284/BBDown-go/internal/config"
	"github.com/QC3284/BBDown-go/internal/entity"
	"github.com/QC3284/BBDown-go/internal/util"
)

// 风控新形态（上游 1.7.3）：HTTP 200 + 合法 JSON + code=0，但载荷里只有 v_voucher
// （"voucher_…"）而没有 dash/durl。识别不到时的表现是「静默零轨道」，用户只看到
// 「任务完成却没有产物」，而且在 sub check 的风控窗口里整批毫秒级失败无原因。

// voucherJSON 用单引号 JSON + 替换成双引号（与仓内既有夹具写法一致，避免转义噪音）。
func voucherJSON(doc string) string {
	return strings.ReplaceAll(doc, "'", string('"'))
}

// TestThrowIfVoucherShapes 钉住三种落点都要认：顶层 / data / result（外加叠加形态
// data.result），而「空串 v_voucher」与「没有该字段」都不算风控。
//
// 变异验证：把 throwIfVoucher 里的任一处判定删掉 → 对应用例红。
func TestThrowIfVoucherShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"顶层 v_voucher", "{'code':0,'v_voucher':'voucher_abc'}", true},
		{"data.v_voucher（上游只认这一处）", "{'code':0,'message':'OK','data':{'v_voucher':'voucher_abc'}}", true},
		{"result.v_voucher（番剧走 result）", "{'code':0,'result':{'v_voucher':'voucher_abc'}}", true},
		{"data.result.v_voucher（叠加形态）", "{'code':0,'data':{'result':{'v_voucher':'voucher_abc'}}}", true},
		{"顶层空串不算", "{'code':0,'v_voucher':''}", false},
		{"data 里空串不算", "{'code':0,'data':{'v_voucher':'   '}}", false},
		{"非字符串不算", "{'code':0,'data':{'v_voucher':123}}", false},
		{"正常响应", "{'code':0,'data':{'dash':{'video':[{'id':80}]}}}", false},
		{"业务错误码（由 throwIfBizError 负责）", "{'code':-404,'message':'啥都木有'}", false},
	}
	for _, c := range cases {
		var root map[string]interface{}
		if err := util.UnmarshalJSON(voucherJSON(c.body), &root); err != nil {
			t.Fatalf("%s: 夹具 JSON 解不开: %v", c.name, err)
		}
		err := throwIfVoucher(root)
		if got := err != nil; got != c.want {
			t.Errorf("%s: throwIfVoucher -> %v, want %v", c.name, err, c.want)
		}
		if err != nil && !errors.Is(err, ErrRiskControlVoucher) {
			t.Errorf("%s: 错误应当可用 errors.Is 判定为风控凭证: %v", c.name, err)
		}
	}
}

// TestVoucherErrorMessageHasGuidance 钉住文案必须带三类处置建议（缺一条用户就只剩
// 「任务完成但没有产物」这一种反馈）。
func TestVoucherErrorMessageHasGuidance(t *testing.T) {
	err := throwIfVoucher(map[string]interface{}{"v_voucher": "voucher_x"})
	if err == nil {
		t.Fatal("应当识别为风控凭证")
	}
	for _, want := range []string{"v_voucher", "人机验证", "等几分钟", "出口 IP", "换账号/设备", "UA 轮换仅对 HTTP 412 生效", "--retry-delay"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("文案缺少 %q：%s", want, err.Error())
		}
	}
}

// extractRawDoc 起一个假 playurl：无论 qn 都返回给定文档，返回解析结果与错误
// （与 extractWithPlayurlDocs 同款装配，区别是这里**允许**解析失败——本用例正是要断言失败）。
func extractRawDoc(t *testing.T, body string) (*entity.ParsedResult, error) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	cfg := config.DefaultAppSettings()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.TvHost = cfg.Host
	cfg.Wbi = "test_wbi_key"
	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	return NewParser(client, cfg).ExtractTracks(context.Background(), "av170001", "170001", "999", "", false, false, false, "", false, "0")
}

// TestExtractTracksVoucherIsReadableAndRetryable 是核心用例：三种形态都必须变成
// **可读错误**（而不是零轨道的空结果），这样 workflow 的页面级重试才会接手
// （那边对任何 ExtractTracks 错误都按 pageRetryLimit=3 + --retry-delay 退避重试）。
//
// 变异验证：删掉 parseDomesticStreams 里的 throwIfVoucher 调用 → 本用例红（会返回空结果且无错误）。
func TestExtractTracksVoucherIsReadableAndRetryable(t *testing.T) {
	shapes := map[string]string{
		"顶层":     "{'code':0,'v_voucher':'voucher_abc'}",
		"data":   "{'code':0,'message':'OK','data':{'v_voucher':'voucher_abc'}}",
		"result": "{'code':0,'result':{'v_voucher':'voucher_abc'}}",
	}
	for name, body := range shapes {
		res, err := extractRawDoc(t, voucherJSON(body))
		if err == nil {
			t.Fatalf("%s：风控响应应当返回错误（否则解析出零轨道、静默失败），实际结果 %+v", name, res)
		}
		if res != nil {
			t.Errorf("%s：报错时不该同时返回解析结果：%+v", name, res)
		}
		if !errors.Is(err, ErrRiskControlVoucher) {
			t.Errorf("%s：错误应当可用 errors.Is(err, ErrRiskControlVoucher) 判定：%v", name, err)
		}
		if !strings.Contains(err.Error(), "人机验证") {
			t.Errorf("%s：错误文案不可读：%v", name, err)
		}
	}
}

// extractIntlBodies 驱动 INTL 双轮解析：按 prefer_code_type 分流返回**内联文档**。
func extractIntlBodies(t *testing.T, routes map[string]string) (*entity.ParsedResult, []string, error) {
	t.Helper()
	var seen []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("prefer_code_type")
		seen = append(seen, key)
		body, ok := routes[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	cfg := config.DefaultAppSettings()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.TvHost = cfg.Host
	cfg.Wbi = "test_wbi_key"
	cfg.Cookie = ""
	cfg.Token = ""
	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)
	res, err := NewParser(client, cfg).ExtractTracks(context.Background(), "av170001", "170001", "999", "", false, true, false, "", false, "0")
	return res, seen, err
}

func intlFixtureBody(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return string(b)
}

// TestIntlVoucherSecondPassDegradesToFirstPass 是双轮的两条臂（与免二压 qn=127 同策略）：
//   - 前轮有轨道 + 后轮 v_voucher → **保留轨道**、打 Warn、不报错；
//   - 两轮都 v_voucher → 抛错（页面级重试接手）。
//
// 变异验证：删掉「保留第一遍轨道」的降级分支（改成 continue）→ 第一臂红（轨道丢失/报错）；
// 删掉末尾的 voucherErr 抛出 → 第二臂红（返回空结果且无错误）。
func TestIntlVoucherSecondPassDegradesToFirstPass(t *testing.T) {
	code0 := intlFixtureBody(t, "intl-code0")
	voucher := voucherJSON("{'code':0,'message':'OK','data':{'v_voucher':'voucher_abc'}}")

	// 第一臂：前轮有轨道、后轮风控。
	var logs bytes.Buffer
	restore := util.RedirectConsoleLogs(&logs)
	res, seen, err := extractIntlBodies(t, map[string]string{"0": code0, "1": voucher})
	restore()

	if err != nil {
		t.Fatalf("后轮被风控但前轮有轨道时不该报错（降级保留轨道）：%v", err)
	}
	if res == nil || len(res.VideoTracks) == 0 {
		t.Fatalf("应当保留第一遍解析出的轨道，实际 %+v", res)
	}
	if len(seen) != 2 || seen[0] != "0" || seen[1] != "1" {
		t.Errorf("两轮都要请求过：%v", seen)
	}
	if !strings.Contains(logs.String(), "保留第一遍") {
		t.Errorf("降级时必须打 Warn 说明轨道不完整，实际日志：%q", logs.String())
	}

	// 第二臂：两轮都被风控 → 抛错（可被页面级重试识别）。
	res2, seen2, err2 := extractIntlBodies(t, map[string]string{"0": voucher, "1": voucher})
	if err2 == nil {
		t.Fatalf("两轮都无轨道时应当报错，实际结果 %+v", res2)
	}
	if !errors.Is(err2, ErrRiskControlVoucher) {
		t.Errorf("错误应当可用 errors.Is 判定为风控凭证：%v", err2)
	}
	if len(seen2) != 2 {
		t.Errorf("两轮都该尝试：%v", seen2)
	}
	if res2 != nil {
		t.Errorf("报错时不该返回结果：%+v", res2)
	}
}

// TestIntlVoucherFirstPassSecondPassWorks 反向：第一轮被风控、第二轮拿到轨道 → 用第二轮的
// 轨道正常返回（不能因为第一轮撞风控就把整次解析判死）。
func TestIntlVoucherFirstPassSecondPassWorks(t *testing.T) {
	code1 := intlFixtureBody(t, "intl-code1")
	voucher := voucherJSON("{'code':0,'message':'OK','data':{'v_voucher':'voucher_abc'}}")

	res, seen, err := extractIntlBodies(t, map[string]string{"0": voucher, "1": code1})
	if err != nil {
		t.Fatalf("第二轮有轨道时不该报错：%v", err)
	}
	if res == nil || len(res.VideoTracks) == 0 {
		t.Fatalf("应当用第二轮的轨道，实际 %+v", res)
	}
	if len(seen) != 2 {
		t.Errorf("两轮都该尝试：%v", seen)
	}
}
