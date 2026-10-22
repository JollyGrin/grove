package tmux

import (
	"reflect"
	"testing"
)

func TestPlanEnter(t *testing.T) {
	dash := FocusPane{ID: "%0", Index: 1}
	mk := func(id string, idx int) FocusPane { return FocusPane{ID: id, Index: idx} }
	tests := []struct {
		name   string
		panes  []FocusPane
		shown  string
		minimz []string
	}{
		{"no chat", []FocusPane{dash}, "", nil},
		{"active chat stays", []FocusPane{dash, mk("%1", 2), {ID: "%2", Index: 3, Active: true}, mk("%3", 4)}, "%2", []string{"%1", "%3"}},
		{"dash active: previous pane stays", []FocusPane{{ID: "%0", Index: 1, Active: true}, mk("%1", 2), {ID: "%2", Index: 3, Last: true}, mk("%3", 4)}, "%2", []string{"%1", "%3"}},
		{"nothing known: last chat", []FocusPane{dash, mk("%1", 2), mk("%2", 3)}, "%2", []string{"%1"}},
		{"rail and remote never minimized", []FocusPane{dash, mk("%1", 2), {ID: "%5", Index: 3, Rail: true}, {ID: "%6", Index: 4, Remote: true}}, "%1", nil},
		{"dash is the lowest index, not 0", []FocusPane{mk("%9", 5), mk("%8", 6)}, "%8", nil},
		{"active dash and rail: no chat", []FocusPane{{ID: "%0", Index: 0, Active: true}, {ID: "%5", Index: 1, Rail: true}}, "", nil},
	}
	for _, tc := range tests {
		shown, min := PlanEnter(tc.panes)
		if shown != tc.shown || !reflect.DeepEqual(min, tc.minimz) {
			t.Errorf("%s: got shown=%q min=%v, want %q %v", tc.name, shown, min, tc.shown, tc.minimz)
		}
	}
}

func TestParseFocusPanes(t *testing.T) {
	out := "%0\t1\t1\t0\t\t\t.\n%1\t2\t0\t1\thostA\t\t.\n%2\t3\t0\t0\t\t1\t.\n"
	got := parseFocusPanes(out)
	want := []FocusPane{
		{ID: "%0", Index: 1, Active: true},
		{ID: "%1", Index: 2, Last: true, Remote: true},
		{ID: "%2", Index: 3, Rail: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func TestAddFocusHid(t *testing.T) {
	l := AddFocusHid(nil, "a")
	l = AddFocusHid(l, "b")
	l = AddFocusHid(l, "a")
	if !reflect.DeepEqual(l, []string{"a", "b"}) {
		t.Errorf("got %v", l)
	}
	if !reflect.DeepEqual(ParseFocusHid(" a  b\n"), []string{"a", "b"}) || len(ParseFocusHid("")) != 0 {
		t.Error("ParseFocusHid")
	}
}
