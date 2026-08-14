package resources

import (
	"testing"
	"time"
)

func TestClassifyJobFailureDeadlineExceeded(t *testing.T) {
	now := time.Now()
	summary := JobAttemptsSummary{
		Failed:                true,
		StartedAt:             now.Add(-10 * time.Minute),
		CompletedAt:           now,
		ActiveDeadlineSeconds: int64Ptr(600),
	}
	if got := ClassifyJobFailure(summary); got != "it failed on the deadline" {
		t.Errorf("ClassifyJobFailure = %q, want the deadline diagnosis", got)
	}
}

func TestClassifyJobFailureUnderDeadlineNotClassified(t *testing.T) {
	now := time.Now()
	summary := JobAttemptsSummary{
		Failed:                true,
		StartedAt:             now.Add(-1 * time.Minute),
		CompletedAt:           now,
		ActiveDeadlineSeconds: int64Ptr(600),
	}
	if got := ClassifyJobFailure(summary); got != "" {
		t.Errorf("ClassifyJobFailure = %q, want \"\" (well under the deadline)", got)
	}
}

func TestClassifyJobFailureNotFailedReturnsEmpty(t *testing.T) {
	summary := JobAttemptsSummary{Failed: false, ActiveDeadlineSeconds: int64Ptr(600)}
	if got := ClassifyJobFailure(summary); got != "" {
		t.Errorf("ClassifyJobFailure = %q, want \"\" for a non-failed Job", got)
	}
}

func TestClassifyJobFailureBackoffExceededSameFailure(t *testing.T) {
	code := int32(1)
	summary := JobAttemptsSummary{
		Failed: true, FailedAttempts: 3, BackoffLimit: 3,
		Attempts: []JobAttempt{
			{Result: JobAttemptFailed, ExitCode: &code},
			{Result: JobAttemptFailed, ExitCode: &code},
		},
	}
	if got := ClassifyJobFailure(summary); got == "" {
		t.Errorf("expected a classification for backoffLimit-exceeded with identical attempts, got \"\"")
	}
}

func TestSameFailureAcrossAttemptsRequiresTwoTerminalAttempts(t *testing.T) {
	code := int32(1)
	if _, ok := SameFailureAcrossAttempts(nil); ok {
		t.Errorf("expected ok=false with no attempts")
	}
	if _, ok := SameFailureAcrossAttempts([]JobAttempt{{Result: JobAttemptFailed, ExitCode: &code}}); ok {
		t.Errorf("expected ok=false with only 1 terminal attempt")
	}
}

func TestSameFailureAcrossAttemptsSameExitCode(t *testing.T) {
	code := int32(1)
	attempts := []JobAttempt{
		{Result: JobAttemptFailed, ExitCode: &code},
		{Result: JobAttemptFailed, ExitCode: &code},
	}
	same, ok := SameFailureAcrossAttempts(attempts)
	if !ok || !same {
		t.Errorf("SameFailureAcrossAttempts = (%v, %v), want (true, true)", same, ok)
	}
}

func TestSameFailureAcrossAttemptsDifferentExitCode(t *testing.T) {
	c1, c2 := int32(1), int32(137)
	attempts := []JobAttempt{
		{Result: JobAttemptFailed, ExitCode: &c1},
		{Result: JobAttemptFailed, ExitCode: &c2},
	}
	same, ok := SameFailureAcrossAttempts(attempts)
	if !ok || same {
		t.Errorf("SameFailureAcrossAttempts = (%v, %v), want (false, true)", same, ok)
	}
}

func TestSameFailureAcrossAttemptsUnknownExitCodeIsUnknown(t *testing.T) {
	code := int32(1)
	attempts := []JobAttempt{
		{Result: JobAttemptFailed, ExitCode: &code},
		{Result: JobAttemptFailed, ExitCode: nil}, // no exit code readable
	}
	if _, ok := SameFailureAcrossAttempts(attempts); ok {
		t.Errorf("expected ok=false when an attempt's exit code can't be read, not a fabricated verdict")
	}
}

func int64Ptr(v int64) *int64 { return &v }
