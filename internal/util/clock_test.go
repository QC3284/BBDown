package util

import (
	"net/http"
	"testing"
	"time"
)

// TestNoteServerDateClampsAndIgnoresGarbage: a drifted local clock breaks WBI
// signing entirely, but a wildly wrong Date header must not become a new failure
// source.
func TestNoteServerDateClampsAndIgnoresGarbage(t *testing.T) {
	old := serverClockOffset.Load()
	defer serverClockOffset.Store(old)
	serverClockOffset.Store(0)

	NoteServerDate("") // absent
	NoteServerDate("not a date")
	if got := ServerClockOffset(); got != 0 {
		t.Errorf("garbage input moved the clock by %ds", got)
	}

	// 30s of drift is corrected.
	NoteServerDate(time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat))
	got := ServerClockOffset()
	if got < 28 || got > 32 {
		t.Errorf("offset = %ds, want ~30", got)
	}

	// Beyond an hour the header is ignored (and the previous value stays).
	serverClockOffset.Store(0)
	NoteServerDate(time.Now().Add(5 * time.Hour).UTC().Format(http.TimeFormat))
	if got := ServerClockOffset(); got != 0 {
		t.Errorf("a 5h skew must be ignored, got %ds", got)
	}
}

// TestMayCalibrateClock: only official hosts may move the offset, and an
// --insecure connection never may (its Date header is attacker-controlled).
func TestMayCalibrateClock(t *testing.T) {
	secure := NewHTTPClient(nil, nil, nil)
	if !secure.mayCalibrateClock("https://api.bilibili.com/x/y") {
		t.Error("an official API host must be allowed to calibrate")
	}
	if !secure.mayCalibrateClock("https://passport.bilibili.com/qrcode") {
		t.Error("a subdomain must be allowed to calibrate")
	}
	if secure.mayCalibrateClock("https://mirror.example/api") {
		t.Error("a mirror must not move the global clock")
	}

	insecure := NewHTTPClient(func() bool { return true }, nil, nil)
	if insecure.mayCalibrateClock("https://api.bilibili.com/x/y") {
		t.Error("an --insecure connection must not calibrate the clock")
	}
}
