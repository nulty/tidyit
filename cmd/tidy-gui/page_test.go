package main

import (
	"testing"

	"tidy/pkg/report"
)

func TestItemSubtitleEscapesMarkup(t *testing.T) {
	it := report.Item{
		Label:  "x",
		Path:   "/h/a&b",
		Detail: "<old>",
		Action: &report.Action{Command: `rm -r "/h/a&b"`},
	}
	want := "&lt;old&gt;\n/h/a&amp;b\n<tt>rm -r &#34;/h/a&amp;b&#34;</tt>"
	if got := itemSubtitle(it); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestItemSubtitleSkipsPathShownInLabel(t *testing.T) {
	it := report.Item{Label: "/h/Downloads/x.iso", Path: "/h/Downloads/x.iso"}
	if got := itemSubtitle(it); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestCheckSubtitle(t *testing.T) {
	r := report.Result{Items: []report.Item{{Label: "a"}, {Label: "b"}, {Label: "c"}, {Label: "d"}}}
	if got, want := checkSubtitle(r), "4 items: a, b, c, …"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	r.Items = r.Items[:1]
	if got, want := checkSubtitle(r), "1 item: a"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
