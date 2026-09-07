package main

import "testing"

// The banner printed "http://localhost" + addr, which is correct only for the
// default ":8080". Binding an explicit host — as the container does — produced
// "http://localhost127.0.0.1:8080", a URL that goes nowhere.
func TestBrowsableURL(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":           "http://localhost:8080",
		"127.0.0.1:8080":  "http://127.0.0.1:8080",
		"0.0.0.0:8080":    "http://localhost:8080",
		"192.168.1.7:443": "http://192.168.1.7:443",
	} {
		if got := browsableURL(addr); got != want {
			t.Errorf("browsableURL(%q) = %q, want %q", addr, got, want)
		}
	}
}
