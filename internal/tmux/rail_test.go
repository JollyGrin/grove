package tmux

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

type geo struct {
	id, rail        string
	left, top, w, h int
}

func railGeometry(t *testing.T, session string) (winW int, panes []geo) {
	t.Helper()
	out, err := run("display-message", "-p", "-t", cockpitWindowTarget(session), "#{window_width}")
	if err != nil {
		t.Fatal(err)
	}
	winW, _ = strconv.Atoi(out)
	out, err = run("list-panes", "-t", cockpitWindowTarget(session), "-F",
		"#{pane_id} #{pane_left} #{pane_top} #{pane_width} #{pane_height} #{pane_pid}")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		var g geo
		var pid int
		if _, err := fmt.Sscan(line, &g.id, &g.left, &g.top, &g.w, &g.h, &pid); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		g.rail = strconv.Itoa(pid)
		panes = append(panes, g)
	}
	return
}

func countRails(t *testing.T, session string) int {
	t.Helper()
	panes, err := cockpitPanes(session)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range panes {
		if p.rail {
			n++
		}
	}
	return n
}

// grove-486: focus owns exactly one rail pane under the chat slot at
// RailHeight; a second ensure reuses it; horizontal kills it and only it.
func TestRailLifecycleAndRetile(t *testing.T) {
	scratchServer(t)
	dir := t.TempDir()
	const s = "grove-r"
	if _, err := run("new-session", "-d", "-s", s, "-n", "cockpit", "-x", "200", "-y", "50", "-c", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := run("split-window", "-t", ExactActive(s), "-c", dir); err != nil { // the chat
		t.Fatal(err)
	}
	before, _ := railGeometry(t, s)
	_ = before

	railID, err := EnsureRail(s, dir, "sleep 600")
	if err != nil {
		t.Fatalf("EnsureRail: %v", err)
	}
	if err := RetileFocus(s); err != nil {
		t.Fatalf("RetileFocus: %v", err)
	}
	if n := countRails(t, s); n != 1 {
		t.Fatalf("rails = %d, want 1", n)
	}
	again, err := EnsureRail(s, dir, "sleep 600")
	if err != nil || again != railID {
		t.Fatalf("second EnsureRail = %q, %v; want reuse of %q", again, err, railID)
	}
	if n := countRails(t, s); n != 1 {
		t.Fatalf("rails after reuse = %d, want 1", n)
	}

	assertShape := func(when string, wantPanes int) []geo {
		winW, panes := railGeometry(t, s)
		if len(panes) != wantPanes {
			t.Fatalf("%s: %d panes, want %d", when, len(panes), wantPanes)
		}
		last := panes[len(panes)-1]
		if last.id != railID {
			t.Errorf("%s: rail %s is not the last pane (%s)", when, railID, last.id)
		}
		if last.h != RailHeight {
			t.Errorf("%s: rail height = %d, want %d", when, last.h, RailHeight)
		}
		if last.left != panes[1].left || last.top <= panes[1].top {
			t.Errorf("%s: rail not stacked under the chat: %+v", when, panes)
		}
		want := winW * cockpitFocusMainWidth / 100
		if d := panes[0].w - want; d < -1 || d > 1 {
			t.Errorf("%s: dash width = %d of %d, want ~%d", when, panes[0].w, winW, want)
		}
		return panes
	}
	assertShape("after ensure", 3)

	// A third pane (a second chat) is spawned the way SpawnPane does.
	if _, err := run("split-window", "-t", ExactActive(s), "-c", dir); err != nil {
		t.Fatal(err)
	}
	if err := RetileFocus(s); err != nil {
		t.Fatal(err)
	}
	assertShape("after a second chat", 4)

	_, chatPanes := railGeometry(t, s)
	var keep []string
	for _, p := range chatPanes {
		if p.id != railID {
			keep = append(keep, p.id+":"+p.rail)
		}
	}
	if err := ApplyLayout(s, "horizontal"); err != nil {
		t.Fatalf("ApplyLayout horizontal: %v", err)
	}
	if n := countRails(t, s); n != 0 {
		t.Fatalf("rails after horizontal = %d, want 0", n)
	}
	_, left := railGeometry(t, s)
	var now []string
	for _, p := range left {
		now = append(now, p.id+":"+p.rail)
	}
	if strings.Join(now, ",") != strings.Join(keep, ",") {
		t.Errorf("horizontal touched other panes: %v -> %v", keep, now)
	}
}

func TestRailPaneClassifiers(t *testing.T) {
	scratchServer(t)
	dir := t.TempDir()
	const s = "grove-q"
	if _, err := run("new-session", "-d", "-s", s, "-n", "cockpit", "-x", "200", "-y", "50", "-c", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := run("split-window", "-t", ExactActive(s), "-c", dir); err != nil {
		t.Fatal(err)
	}
	railID, err := EnsureRail(s, dir, "sleep 600")
	if err != nil {
		t.Fatal(err)
	}
	if !IsRailPane(railID) {
		t.Error("IsRailPane(rail) = false")
	}
	if err := PaneClosable(railID, nil); err != ErrRail {
		t.Errorf("PaneClosable(rail) = %v, want ErrRail", err)
	}
	pane, win, pid, ok := RailPane(s)
	if !ok || pane != railID || !strings.HasPrefix(win, "@") || pid != 0 {
		t.Errorf("RailPane = %q %q %d %v (no stamp yet: pid 0)", pane, win, pid, ok)
	}
	// grove-521: a stamp that is the pane's own process is the pid to wake;
	// any other stamp (a viewer under a shell) is never signalled.
	panePID, _ := run("display-message", "-p", "-t", railID, "#{pane_pid}")
	want, _ := strconv.Atoi(strings.TrimSpace(panePID))
	if err := StampRailPID(railID, want); err != nil {
		t.Fatal(err)
	}
	if _, _, pid, _ := RailPane(s); pid != want || want == 0 {
		t.Errorf("RailPane pid = %d, want the pane's own %d", pid, want)
	}
	if err := StampRailPID(railID, want+1); err != nil {
		t.Fatal(err)
	}
	if _, _, pid, _ := RailPane(s); pid != 0 {
		t.Errorf("RailPane pid = %d for a stamp that is not the pane's process, want 0", pid)
	}
	out, _ := run("list-panes", "-a", "-F", paneListFormat)
	rails := 0
	for _, p := range ParsePanes(out) {
		if p.Rail {
			rails++
			if p.Pane != railID {
				t.Errorf("wrong pane flagged as rail: %s", p.Pane)
			}
		}
	}
	if rails != 1 {
		t.Errorf("ParsePanes flagged %d rails, want 1", rails)
	}
	bf, _ := run("display-message", "-p", "-t", railID, "-F", "#{@grove_rail}")
	if bf != "1" {
		t.Errorf("@grove_rail = %q, want 1", bf)
	}
}
