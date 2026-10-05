package markdown

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestImportMetaAcceptsLooseFrontMatter(t *testing.T) {
	var meta importMeta
	raw := `{"title":2024,"categories":"技术","tags":["go",1,["a","b"]],"date":"2024-01-01"}`
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Title != "2024" || !reflect.DeepEqual(meta.Categories, []string{"技术"}) ||
		!reflect.DeepEqual(meta.Tags, []string{"go", "1", "a", "b"}) {
		t.Fatalf("unexpected meta: %+v", meta)
	}
}
