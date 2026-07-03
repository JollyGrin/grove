package tmux

import (
	"testing"
)

func TestSessionName(t *testing.T) {
	tests := []struct {
		repo string
		want string
	}{
		{"myrepo", "pr-myrepo"},
		{"my.repo", "pr-my-repo"},
		{"repo:name", "pr-repo-name"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := SessionName(tt.repo)
			if got != tt.want {
				t.Errorf("SessionName(%q) = %q, want %q", tt.repo, got, tt.want)
			}
		})
	}
}

func TestWindowName(t *testing.T) {
	tests := []struct {
		wt   string
		want string
	}{
		{"feat-x", "feat-x"},
		{"fix:bug", "fix-bug"},
		{"task.123", "task-123"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := WindowName(tt.wt)
			if got != tt.want {
				t.Errorf("WindowName(%q) = %q, want %q", tt.wt, got, tt.want)
			}
		})
	}
}

func TestWindowTarget(t *testing.T) {
	got := WindowTarget("myrepo", "feat-x")
	want := "pr-myrepo:feat-x"
	if got != want {
		t.Errorf("WindowTarget = %q, want %q", got, want)
	}
}

func TestPaneTarget(t *testing.T) {
	got := PaneTarget("myrepo", "feat-x", 1)
	want := "pr-myrepo:feat-x.1"
	if got != want {
		t.Errorf("PaneTarget = %q, want %q", got, want)
	}
}

func TestSessionExistsNonexistent(t *testing.T) {
	// A session with this name should never exist
	if SessionExists("pr-test-nonexistent-session-xyz") {
		t.Error("expected false for nonexistent session")
	}
}

func TestWindowExistsNonexistent(t *testing.T) {
	if WindowExists("pr-test-nonexistent-session-xyz", "some-window") {
		t.Error("expected false for nonexistent window")
	}
}

func TestCapturePaneNonexistent(t *testing.T) {
	out, err := CapturePane("pr-test-nonexistent-session-xyz:0.0")
	if err != nil {
		t.Errorf("expected nil error for nonexistent pane, got %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output, got %q", out)
	}
}
