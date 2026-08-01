package session

import (
	"context"
	"testing"
)

// SaveSettings must refuse a threshold set the config layer rejects, so an
// invalid warning configuration can never reach disk or the running session.
func TestSaveSettingsRejectsInvalidUsageThresholds(t *testing.T) {
	s := openProjectForSettings(t)

	settings := s.Settings()
	settings.Usage.ContextWarnPercent = 95
	settings.Usage.ContextCriticalPercent = 80 // critical must exceed warning
	if err := s.SaveSettings(settings); err == nil {
		t.Fatal("expected SaveSettings to reject critical <= warning")
	}

	// The rejected values must not have been adopted by the live session.
	if got := s.Settings().Usage.ContextCriticalPercent; got == 80 {
		t.Errorf("invalid critical percent was persisted: %g", got)
	}
}

func TestSaveSettingsPersistsValidUsageThresholds(t *testing.T) {
	s := openProjectForSettings(t)

	amount := 5.0
	settings := s.Settings()
	settings.Usage.ContextWarnPercent = 60
	settings.Usage.ContextCriticalPercent = 85
	settings.Usage.CostWarnAmount = &amount
	if err := s.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got := s.Settings().Usage
	if got.ContextWarnPercent != 60 || got.ContextCriticalPercent != 85 {
		t.Errorf("percents = %g/%g, want 60/85", got.ContextWarnPercent, got.ContextCriticalPercent)
	}
	if got.CostWarnAmount == nil || *got.CostWarnAmount != amount {
		t.Errorf("cost warn amount = %v, want %g", got.CostWarnAmount, amount)
	}
}

func openProjectForSettings(t *testing.T) *Session {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	return s
}
