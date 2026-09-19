package util

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 本文件搬上游 UpowerGuardTests / SessdataExpiryTests / ClockCalibrationTests 三张表。

func TestUpstreamUpowerGuard(t *testing.T) {
	// 充电专属且无播放权限：判为试看，理由里给出实际/完整时长（hh:mm:ss）
	v := InspectUpower(true, false, 8628, 389)
	if !v.IsPreview {
		t.Error("充电专属且无权限应判为试看")
	}
	for _, want := range []string{"充电权限", "00:06:29", "02:23:48"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("理由缺 %q: %s", want, v.Reason)
		}
	}

	if v := InspectUpower(true, true, 8628, 8628); v.IsPreview {
		t.Errorf("有充电权限不应判为试看: %s", v.Reason)
	}

	v = InspectUpower(false, false, 3600, 300)
	if !v.IsPreview || !strings.Contains(v.Reason, "试看片段") {
		t.Errorf("时长落差应判为试看: %+v", v)
	}

	if v := InspectUpower(false, false, 3600, 3590); v.IsPreview {
		t.Errorf("10 秒漂移不应判为试看: %s", v.Reason)
	}
	if v := InspectUpower(false, false, 60, 40); v.IsPreview {
		t.Errorf("绝对差未达 30 秒，不应判为试看: %s", v.Reason)
	}

	if v := InspectUpower(false, false, 0, 0); v.IsPreview {
		t.Errorf("时长未知不应判为试看: %s", v.Reason)
	}
	if v := InspectUpower(true, false, 0, 0); !v.IsPreview || !strings.Contains(v.Reason, "充电权限") {
		t.Errorf("充电专属无权限即使时长未知也应告警: %+v", v)
	}
}

// buildSessdata 已在 sessdata_test.go 中定义（同一套构造），这里直接复用。

func TestUpstreamSessdataExpiry(t *testing.T) {
	// 10 天后过期 → 估算 9~10 天（容差 1 天）
	days := EstimateSessdataExpiryDays("SESSDATA=" + buildSessdata(time.Now().AddDate(0, 0, 10).Unix()))
	if days == nil {
		t.Fatal("有效 SESSDATA 应给出估算天数")
	}
	if *days < 9 || *days > 10 {
		t.Errorf("估算天数 = %d，上游期望 9~10", *days)
	}

	// 已过期 → 非正数
	if d := EstimateSessdataExpiryDays("SESSDATA=" + buildSessdata(time.Now().AddDate(0, 0, -1).Unix())); d == nil || *d > 0 {
		t.Errorf("过期 SESSDATA 应给出非正天数，实际 %v", d)
	}

	// 无法判定一律 fail-open 返回 nil（不误报）
	for _, c := range []string{
		"bili_jct=abc; DedeUserID=123",
		"SESSDATA=!!!not-base64!!!%2Cb%2Cc",
		"SESSDATA=" + base64.StdEncoding.EncodeToString([]byte(`{"id":1}`)) + "%2Cb%2Cc",
		"SESSDATA=",
		"",
	} {
		if d := EstimateSessdataExpiryDays(c); d != nil {
			t.Errorf("cookie=%q 应返回 nil，实际 %d", c, *d)
		}
	}
}

func TestUpstreamClockCalibration(t *testing.T) {
	// 用一个明确落在 ±1h 内的偏移（3 分钟），避免与「超过 1 小时即忽略」的规则冲突。
	NoteServerDate("") // 清空：空头是 no-op，先确保起点未知
	skewSeconds := (3 * time.Minute).Seconds()
	NoteServerDate(time.Now().Add(time.Duration(skewSeconds) * time.Second).UTC().Format(http.TimeFormat))
	off := Now().Sub(time.Now())
	if off < 2*time.Minute || off > 4*time.Minute {
		t.Errorf("Date 头未写入时钟偏移：Now 与本地相差 %v，期望约 3 分钟", off)
	}
	if d := time.Duration(UnixNow()-time.Now().Unix()) * time.Second; d < 2*time.Minute || d > 4*time.Minute {
		t.Errorf("UnixNow 未带偏移: %v", d)
	}

	// 畸形 Date 头不改动已有偏移
	before := ServerClockOffset()
	NoteServerDate("not a date")
	if ServerClockOffset() != before {
		t.Errorf("畸形 Date 头改动了偏移: %d → %d", before, ServerClockOffset())
	}

	// 超出 ±1h 的偏移被忽略（本地时钟本来就是秒级误差，一个小时的「校准」只会更糟）
	NoteServerDate(time.Now().Add(2 * time.Hour).UTC().Format(http.TimeFormat))
	if ServerClockOffset() == before+7200 || ServerClockOffset() == 7200 {
		t.Errorf("超过 1 小时的偏移不应写全局，实际 %d", ServerClockOffset())
	}
}
