package login

import "testing"

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
