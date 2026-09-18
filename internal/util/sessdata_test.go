package util

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func buildSessdata(expiryUnix int64) string {
	json := fmt.Sprintf("{\"id\":1,\"expires\":%d}", expiryUnix)
	b64 := base64.StdEncoding.EncodeToString([]byte(json))
	return b64 + "%2Csecond%2Cthird"
}

// TestEstimateSessdataExpiry mirrors the upstream cases: a parseable value gives
// an approximate number of days, an expired one is non-positive, and anything
// unparseable fails open (nil) instead of warning.
func TestEstimateSessdataExpiry(t *testing.T) {
	soon := EstimateSessdataExpiryDays("SESSDATA=" + buildSessdata(time.Now().AddDate(0, 0, 10).Unix()))
	if soon == nil {
		t.Fatal("a parseable SESSDATA must yield an estimate")
	}
	if *soon < 9 || *soon > 10 {
		t.Errorf("days = %d, want 9..10", *soon)
	}

	expired := EstimateSessdataExpiryDays("SESSDATA=" + buildSessdata(time.Now().AddDate(0, 0, -1).Unix()))
	if expired == nil || *expired > 0 {
		t.Errorf("an expired SESSDATA must report a non-positive value, got %v", expired)
	}

	// Fail-open cases: no SESSDATA at all, and garbage payloads.
	if days := EstimateSessdataExpiryDays("bili_jct=abc; DedeUserID=123"); days != nil {
		t.Errorf("a cookie without SESSDATA must yield nil, got %v", *days)
	}
	if days := EstimateSessdataExpiryDays("SESSDATA=!!!not-base64!!!%2Cb%2Cc"); days != nil {
		t.Errorf("unparseable base64 must yield nil, got %v", *days)
	}
	if days := EstimateSessdataExpiryDays("SESSDATA=%%%"); days != nil {
		t.Errorf("an undecodable value must yield nil, got %v", *days)
	}
	if days := EstimateSessdataExpiryDays(""); days != nil {
		t.Errorf("an empty cookie must yield nil, got %v", *days)
	}
}

// TestSessdataValueFindsFieldRegardlessOfCaseAndPosition.
func TestSessdataValueFindsField(t *testing.T) {
	if got := sessdataValue("a=1; sessdata=x%2Cy; b=2"); got != "x%2Cy" {
		t.Errorf("sessdataValue = %q", got)
	}
	if got := sessdataValue("nosessdata=1"); got != "" {
		t.Errorf("sessdataValue = %q, want empty", got)
	}
}
