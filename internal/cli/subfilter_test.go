package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/QC3284/BBDown/internal/substore"
)

// F6 订阅增强：sub add --filter 存一个稿件标题正则，sub check 时按标题应用。

// withTempSubStore 把订阅清单指到临时目录，避免用例碰到真实清单。
func withTempSubStore(t *testing.T) {
	t.Helper()
	old := substore.StoreRoot
	substore.StoreRoot = t.TempDir()
	t.Cleanup(func() { substore.StoreRoot = old })
}

// captureStdout 把 os.Stdout 换成管道，收集 fn 期间打印的内容（util.Log 写 stdout）。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = old
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestSubAddRejectsInvalidFilterAtAddTime 钉住「非法正则在 add 当场报错」（F6）：
// 不在 add 时校验的话，坏订阅会先落盘，直到 sub check 才以「跳过」的形式暴露。
//
// 变异验证：subAddCmd 改回 substore.Add(args[0], optSubName)（丢弃 --filter），
// 本用例拿不到错误、且清单里会出现一条订阅，变红。
func TestSubAddRejectsInvalidFilterAtAddTime(t *testing.T) {
	withTempSubStore(t)
	optSubFilter = "["
	t.Cleanup(func() { optSubFilter = "" })

	err := subAddCmd.RunE(subAddCmd, []string{"mid:1"})
	if err == nil {
		t.Fatal("非法过滤正则在 sub add 时必须报错")
	}
	if !strings.Contains(err.Error(), "过滤正则") {
		t.Errorf("报错要说清是过滤正则的问题: %v", err)
	}
	subs, lerr := substore.ListSorted()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(subs) != 0 {
		t.Fatalf("非法正则不得落盘: %+v", subs)
	}
}

// TestSubAddStoresFilterAndListShowsIt: sub add --filter 写进清单，sub list 能看见
// （看不见的话用户没法确认订阅到底带了什么过滤）。
func TestSubAddStoresFilterAndListShowsIt(t *testing.T) {
	withTempSubStore(t)
	optSubFilter = "^【教程】"
	t.Cleanup(func() { optSubFilter = "" })

	if err := subAddCmd.RunE(subAddCmd, []string{"mid:1"}); err != nil {
		t.Fatal(err)
	}
	subs, err := substore.ListSorted()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Filter != "^【教程】" {
		t.Fatalf("过滤条件没有存下来: %+v", subs)
	}

	out := captureStdout(t, func() {
		if err := subListCmd.RunE(subListCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "^【教程】") {
		t.Errorf("sub list 要看得到过滤条件: %q", out)
	}
}

// TestSubNewAidsAppliesTitleFilter 是 sub check 的选片逻辑：标题不匹配过滤的稿件整条跳过、
// 匹配的照常按历史去重、正则非法时报错（而不是当成不过滤把稿件全下下来）。
//
// 变异验证：把 subNewAids 里的过滤分支删掉（退回只看历史），「标题不匹配」与
// 「正则非法」两条会变红。
func TestSubNewAidsAppliesTitleFilter(t *testing.T) {
	cases := []struct {
		name    string
		sub     substore.Subscription
		title   string
		aids    []string
		history []string
		want    []string
		wantErr bool
	}{
		{
			name:    "无过滤：只挑历史里没有的",
			sub:     substore.Subscription{Target: "mid:1"},
			title:   "任意标题",
			aids:    []string{"1", "2", "3"},
			history: []string{"1"},
			want:    []string{"2", "3"},
		},
		{
			name:  "标题匹配过滤：放行",
			sub:   substore.Subscription{Target: "mid:1", Filter: "教程"},
			title: "【教程】第一课",
			aids:  []string{"1"},
			want:  []string{"1"},
		},
		{
			name:  "标题不匹配过滤：整稿跳过",
			sub:   substore.Subscription{Target: "mid:1", Filter: "教程"},
			title: "【实况】随便玩玩",
			aids:  []string{"1", "2"},
			want:  nil,
		},
		{
			name:    "正则非法：报错而不是全放行",
			sub:     substore.Subscription{Target: "mid:1", Filter: "["},
			title:   "任意标题",
			aids:    []string{"1"},
			wantErr: true,
		},
	}
	for _, c := range cases {
		got, err := subNewAids(c.sub, c.title, c.aids, c.history)
		if c.wantErr {
			if err == nil {
				t.Fatalf("%s: 非法正则必须报错——当成不过滤会把用户明确排除的稿件也下下来", c.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
