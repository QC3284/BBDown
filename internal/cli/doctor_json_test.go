package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
)

// doctor --json：机读形态（脚本/监控用）。字段名小写、可被 json.Unmarshal 解析、退出码语义与文本模式一致。
//
// 变异验证：把 doctorResult 的 json tag 删掉（键变成 Name/Level/Detail）→ 本用例变红。
func TestDoctorJSONIsMachineReadable(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll("{'code':0,'data':{'isLogin':true,'uname':'t','vipStatus':0}}", "'", string('"'))))
	}))
	defer srv.Close()

	cfg := config.DefaultMyOption()
	cfg.Host = strings.TrimPrefix(srv.URL, "https://")
	cfg.WorkDir = t.TempDir()
	client := util.NewHTTPClient(func() bool { return true }, func() string { return "" }, nil)

	orig := doctorChecks
	doctorChecks = []func(context.Context, config.MyOption, *util.HTTPClient) doctorResult{checkAPIAndLogin}
	t.Cleanup(func() { doctorChecks = orig })

	var buf bytes.Buffer
	code := runDoctorJSON(context.Background(), cfg, client, &buf)
	if code != 0 {
		t.Errorf("全部 ok 时退出码应为 0，实际 %d（输出 %s）", code, buf.String())
	}
	var got []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("输出不是合法 JSON：%v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0]["level"] != "ok" || got[0]["name"] == "" || got[0]["detail"] == "" {
		t.Errorf("JSON 字段不符（需要小写键 name/level/detail）：%v", got)
	}
}
