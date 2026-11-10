package report

import "testing"

func TestSafeTotalCountsEachPlaceOnce(t *testing.T) {
	res := []Result{
		{Items: []Item{
			{Path: "/h/.cache/yarn", Size: 300, Confidence: ConfidenceSafe},
			{Path: "/h/.cache/thumbnails", Size: 50, Confidence: ConfidenceSafe},
			{Path: "/h/Downloads/x.iso", Size: 1000, Confidence: ConfidenceReview}, // not safe
			{Label: "trash", Size: 7, Confidence: ConfidenceSafe},                  // no path
		}},
		{Items: []Item{
			{Path: "/h/.cache/yarn/", Size: 280, Confidence: ConfidenceSafe},            // same place
			{Path: "/h/.cache/thumbnails/normal", Size: 40, Confidence: ConfidenceSafe}, // nested
			{Path: "/h/.cache/thumbnails-x", Size: 5, Confidence: ConfidenceSafe},       // sibling, not nested
		}},
	}
	if got, want := SafeTotal(res), int64(300+50+7+5); got != want {
		t.Fatalf("SafeTotal = %d, want %d", got, want)
	}
}
