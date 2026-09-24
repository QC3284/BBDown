package parser

import "testing"

// 每条轨道的其余候选地址（playurl 的 backup_url）必须被收下来：此前只保留被选中的那一个，
// 其余直接丢弃——镜像漏对象或连不上时，下载器没有任何地址可换（本仓有意差异，§4.33 的多候选扩展）。
//
// 变异验证：把 urlCandidates 的返回值退回「只给首选」即变红。
func TestFixtureKeepsBackupURLCandidates(t *testing.T) {
	result, err := extractFixture(t, "backup-url-candidates", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.VideoTracks) != 1 {
		t.Fatalf("video tracks = %d, want 1", len(result.VideoTracks))
	}

	v := result.VideoTracks[0]
	// 首选仍是既有规则：第一个不含显式端口（PCDN 直连）的地址。
	if v.BaseURL != "https://upos-sz-mirrorcoso1.bilivideo.com/v.m4s" {
		t.Errorf("BaseURL = %q，期望首个非 PCDN 地址", v.BaseURL)
	}
	// 其余非 PCDN 地址按原始顺序在前，带显式端口的直连地址排最后：首选规则避开它们，
	// 但作为最后的兜底仍比"没有地址可换"强。
	wantVideo := []string{
		"https://upos-sz-mirrorali.bilivideo.com/v.m4s",
		"https://upos-sz-mirrorqn.bilivideo.com/v.m4s",
		"https://x.mcdn.bilivideo.cn:8082/v.m4s",
	}
	if len(v.BackupURLs) != len(wantVideo) {
		t.Fatalf("视频候选 = %v，期望 %v", v.BackupURLs, wantVideo)
	}
	for i, want := range wantVideo {
		if v.BackupURLs[i] != want {
			t.Errorf("视频候选[%d] = %q，期望 %q（顺序即回退顺序）", i, v.BackupURLs[i], want)
		}
	}

	if len(result.AudioTracks) != 2 {
		t.Fatalf("audio tracks = %d, want 2", len(result.AudioTracks))
	}
	a := result.AudioTracks[0]
	if len(a.BackupURLs) != 1 || a.BackupURLs[0] != "https://upos-sz-mirrorali.bilivideo.com/a.m4s" {
		t.Errorf("音频候选 = %v，期望只剩去重后的另一个镜像", a.BackupURLs)
	}
	// 没有 backup_url 的轨道不该凭空多出候选（否则会让下载器白白重试同一个地址）。
	if n := len(result.AudioTracks[1].BackupURLs); n != 0 {
		t.Errorf("无 backup_url 的轨道候选 = %v，期望空", result.AudioTracks[1].BackupURLs)
	}
}
