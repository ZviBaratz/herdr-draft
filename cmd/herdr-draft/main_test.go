package main

import "testing"

func TestVersionString(t *testing.T) {
	if version() == "" {
		t.Fatal("version() must be non-empty")
	}
}
