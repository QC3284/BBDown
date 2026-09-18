package login

import (
	"strings"
	"testing"
)

// TestValidateAccessToken: an empty token used to be written straight into
// BBDownTV.data, leaving a credential file that looks valid but is not.
func TestValidateAccessToken(t *testing.T) {
	if err := validateAccessToken(""); err == nil {
		t.Error("an empty access_token must be rejected")
	}
	if err := validateAccessToken("   "); err == nil {
		t.Error("a blank access_token must be rejected")
	}
	if err := validateAccessToken("abc123"); err != nil {
		t.Errorf("a real token must pass, got %v", err)
	}
}

// TestCheckLoginPollCode: a non-zero top-level code is an API-level failure and
// must not be mistaken for a successful poll.
func TestCheckLoginPollCode(t *testing.T) {
	if err := checkLoginPollCode(0); err != nil {
		t.Errorf("code 0 must pass, got %v", err)
	}
	for _, code := range []int{-412, 86038, 1} {
		err := checkLoginPollCode(code)
		if err == nil {
			t.Errorf("code %d must be rejected", code)
			continue
		}
		if !strings.Contains(err.Error(), "code=") {
			t.Errorf("the error should carry the API code, got %v", err)
		}
	}
}
