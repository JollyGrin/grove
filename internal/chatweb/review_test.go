package chatweb_test

import (
	"reflect"
	"testing"

	"github.com/JollyGrin/grove/internal/chatweb"
)

// grove-334: the Submit page carries the picks it is asking to submit.
func TestWithReview(t *testing.T) {
	capture := fixture(t, "multi-review")
	got := chatweb.WithReview(chatweb.DetectPicker(capture), capture)
	want := []string{"Which colors do you like? → Red, Blue"}
	if !reflect.DeepEqual(got.Review, want) {
		t.Fatalf("review = %q, want %q", got.Review, want)
	}
	// Any other page has no review block.
	single := fixture(t, "single")
	if r := chatweb.WithReview(chatweb.DetectPicker(single), single).Review; r != nil {
		t.Errorf("single-select page has no review, got %q", r)
	}
	// No picker, no review — even if the text is on screen.
	if r := chatweb.WithReview(chatweb.Picker{}, capture).Review; r != nil {
		t.Errorf("undetected picker got a review %q", r)
	}
}
