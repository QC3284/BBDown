package cli

import "testing"

// 本文件搬上游 ServeCommandTests 的 token 解析表：BBDOWN_SERVE_TOKEN 优先于 --serve-token，
// 环境变量为空串时回落到旗标。

func TestUpstreamResolveServeToken(t *testing.T) {
	cases := []struct {
		cli, env, want string
	}{
		{"", "", ""},
		{"my-cli-token", "", "my-cli-token"},
		{"", "my-env-token", "my-env-token"},
		{"my-cli-token", "new-env-token", "new-env-token"}, // 环境变量优先
		{"same-token", "same-token", "same-token"},
		{"my-cli-token", "", "my-cli-token"},
		{"", "my-env-token", "my-env-token"},
	}
	for _, c := range cases {
		if got := resolveServeToken(c.cli, c.env); got != c.want {
			t.Errorf("resolveServeToken(%q, %q) = %q，上游期望 %q", c.cli, c.env, got, c.want)
		}
	}
}
