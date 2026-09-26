package server

import (
	"embed"
	"net/http"

	"github.com/QC3284/BBDown/internal/util"
)

// 任务 N：serve 的极简 Web UI。页面用 go:embed 打进二进制——单文件 HTML（内联 CSS/JS）、
// 不引前端框架、不引 CDN，离线（内网 / 容器 / 无外网）也能打开，这是本仓相对两条 C# 线
// 最明确的差异化能力。
//
// 用 all: 前缀整目录嵌入（而不是直接嵌 index.html）：同目录的 .keep 保证「页面被删掉」时
// 编译仍然通过，页面缺失由 handleIndex 在运行时明确回 500 并告警——回归用例因此能把它判成
// **断言红**，而不是让 go:embed 把「页面不在二进制里」变成构建错误。
//
//go:embed all:webui
var webUIAssets embed.FS

// webUIPagePath 是唯一的页面资源（单文件，无其它静态资源）。
const webUIPagePath = "webui/index.html"

// handleIndex 返回 Web UI（GET /）。
//
// "/" 模式会兜住所有未注册路径，所以这里把非 "/" 的请求交回 http.NotFound：加页面之前
// 未注册路径得到的就是 404 + "404 page not found"，行为逐字不变。
func (s *APIServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	page, err := webUIAssets.ReadFile(webUIPagePath)
	if err != nil || len(page) == 0 {
		util.LogWarn("Web UI 资源缺失（%s）：二进制里没有内嵌页面", webUIPagePath)
		http.Error(w, `{"error":"web UI asset missing"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}
