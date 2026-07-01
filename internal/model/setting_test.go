package model

import "testing"

func TestSettingValidateStatsSaveIntervalRequiresPositiveInteger(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		setting := Setting{Key: SettingKeyStatsSaveInterval, Value: value}
		if err := setting.Validate(); err == nil {
			t.Fatalf("expected stats save interval %q to fail validation", value)
		}
	}

	setting := Setting{Key: SettingKeyStatsSaveInterval, Value: "1"}
	if err := setting.Validate(); err != nil {
		t.Fatalf("expected positive stats save interval to pass, got %v", err)
	}
}

func TestIsKnownSettingKey(t *testing.T) {
	if !IsKnownSettingKey(SettingKeyStatsSaveInterval) {
		t.Fatalf("expected default setting key to be known")
	}
	if IsKnownSettingKey(SettingKey("unknown_setting")) {
		t.Fatalf("expected unknown setting key to be rejected")
	}
}
