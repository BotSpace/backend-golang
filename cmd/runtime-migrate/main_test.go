package main

import "testing"

func TestParseIDs(t *testing.T) {
	ids, err := parseIDs("1187, 1193,1187")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 1187 || ids[1] != 1193 {
		t.Fatalf("ids = %#v", ids)
	}
	for _, raw := range []string{"", "abc", "-1"} {
		if _, err := parseIDs(raw); err == nil {
			t.Fatalf("parseIDs(%q) must fail", raw)
		}
	}
}
