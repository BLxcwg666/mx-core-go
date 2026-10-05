package imagesync

import (
	"reflect"
	"testing"
)

func TestExtractLocalImageURLsKeepsWholeURL(t *testing.T) {
	text := "![a](https://api.example.com/api/v2/objects/image/a.png) ![b](/api/v2/files/image/b.jpg \"t\") ![c](/objects/image/c.webp)"
	got := ExtractLocalImageURLs(text)
	want := []string{
		"https://api.example.com/api/v2/objects/image/a.png",
		"/api/v2/files/image/b.jpg",
		"/objects/image/c.webp",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
