package vfs

import "testing"

func TestProfileExpansionGolden(t *testing.T) {
	custom := Options{CacheMode: "writes", CacheMaxSize: "123MiB", CacheMaxAge: "7m", DirCacheTime: "9m", PollInterval: "11s"}
	got, err := Expand(ProfileCustom, custom)
	if err != nil {
		t.Fatal(err)
	}
	if got != custom {
		t.Fatalf("custom profile mutated explicit values: got=%+v want=%+v", got, custom)
	}
	tests := map[string]Options{
		ProfileStreaming: {CacheMode: "full", CacheMaxSize: "4GiB", CacheMaxAge: "24h", DirCacheTime: "1h", PollInterval: "15s"},
		ProfileBalanced:  {CacheMode: "full", CacheMaxSize: "2GiB", CacheMaxAge: "12h", DirCacheTime: "30m", PollInterval: "30s"},
		ProfileOffline:   {CacheMode: "full", CacheMaxSize: "8GiB", CacheMaxAge: "168h", DirCacheTime: "24h", PollInterval: "1m"},
		ProfileMinimal:   {CacheMode: "minimal", CacheMaxSize: "512MiB", CacheMaxAge: "1h", DirCacheTime: "5m", PollInterval: "1m"},
	}
	for name, want := range tests {
		got, err := Expand(name, custom)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s expansion=%+v want=%+v", name, got, want)
		}
	}
}

func TestInvalidProfileFailsClosed(t *testing.T) {
	if _, err := Expand("turbo", Options{}); err == nil {
		t.Fatal("expected invalid profile failure")
	}
}
