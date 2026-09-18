package parser

import "testing"

// TestIsVipRestricted: the JSON root message is authoritative; the bare substring
// only backs up non-JSON bodies, so re-encoding or rewording the message cannot
// silently disable the web-page fallback.
func TestIsVipRestricted(t *testing.T) {
	if !isVipRestricted("{\"code\":-10403,\"message\":\"大会员专享限制\",\"data\":null}") {
		t.Error("a JSON body whose message carries the notice must be detected")
	}
	if !isVipRestricted("<html>大会员专享限制</html>") {
		t.Error("a non-JSON body must still be detected by substring")
	}
	if isVipRestricted("{\"code\":0,\"message\":\"success\",\"data\":{}}") {
		t.Error("an ordinary success response must not be treated as VIP-restricted")
	}
	if isVipRestricted("") {
		t.Error("an empty body must not be treated as VIP-restricted")
	}
}
