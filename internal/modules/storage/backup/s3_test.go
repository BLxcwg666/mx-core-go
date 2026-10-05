package backup

import (
	"testing"

	appcfg "github.com/mx-space/core/internal/config"
)

func TestKeyFromURLRoundTrip(t *testing.T) {
	cases := []appcfg.S3Options{
		{Bucket: "b", Region: "r", AccessKeyID: "a", SecretAccessKey: "s", Endpoint: "https://s3.example.com", PathStyleAccess: true},
		{Bucket: "b", Region: "r", AccessKeyID: "a", SecretAccessKey: "s", Endpoint: "https://s3.example.com"},
		{Bucket: "b", Region: "r", AccessKeyID: "a", SecretAccessKey: "s", CustomDomain: "https://cdn.example.com"},
	}
	for _, opts := range cases {
		u, err := newS3Uploader(opts)
		if err != nil {
			t.Fatal(err)
		}
		key := "images/2026/a b.png"
		got, ok := u.keyFromURL(u.publicURL(key))
		if !ok || got != key {
			t.Fatalf("%+v: got %q %v", opts, got, ok)
		}
		if _, ok := u.keyFromURL("https://evil.example.com/other/a.png"); ok {
			t.Fatalf("%+v: foreign url accepted", opts)
		}
	}
}
