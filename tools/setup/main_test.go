package main

import (
	"encoding/json"
	"testing"
)

func TestBootstrapAdmin(t *testing.T) {
	cases := map[string]string{
		`{"owner":{"id":"1"}}`:                                "1",
		`{"owner":{"id":"999"},"team":{"owner_user_id":"2"}}`: "2",
		`{"team":{"owner_user_id":""},"owner":{"id":"3"}}`:    "3",
		`{}`: "",
	}
	for body, want := range cases {
		var app application
		if err := json.Unmarshal([]byte(body), &app); err != nil {
			t.Fatal(err)
		}
		if got := bootstrapAdmin(app); got != want {
			t.Errorf("%s: got %q, want %q", body, got, want)
		}
	}
}
