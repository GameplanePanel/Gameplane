package handlers

import "testing"

func TestValidProjectID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"", false},
		{".", false},
		{"..", false},
		{"123", true},
		{"sodium", true},
		{"a..b", true},
	}
	for _, c := range cases {
		if got := validProjectID(c.id); got != c.want {
			t.Errorf("validProjectID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}
