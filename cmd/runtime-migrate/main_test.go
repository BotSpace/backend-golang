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

func TestParseLegacyProjects(t *testing.T) {
	items, err := parseLegacyProjects("1193=9682a68e-1d0a-4078-929c-2942bb5401d2")
	if err != nil {
		t.Fatal(err)
	}
	if got := items[1193].String(); got != "9682a68e-1d0a-4078-929c-2942bb5401d2" {
		t.Fatalf("project UUID = %q", got)
	}
	for _, raw := range []string{"1193", "abc=9682a68e-1d0a-4078-929c-2942bb5401d2", "1193=invalid"} {
		if _, err := parseLegacyProjects(raw); err == nil {
			t.Fatalf("parseLegacyProjects(%q) must fail", raw)
		}
	}
}
