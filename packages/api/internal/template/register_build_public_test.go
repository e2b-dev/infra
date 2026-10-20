package template

import (
	"os"
	"testing"
)

func TestTemplatesDefaultPublic(t *testing.T) {
	cases := []struct {
		val  string
		set  bool
		want bool
	}{
		{set: false, want: false},
		{val: "true", set: true, want: true},
		{val: "1", set: true, want: true},
		{val: "false", set: true, want: false},
		{val: "", set: true, want: false},
		{val: "garbage", set: true, want: false},
	}
	for _, c := range cases {
		if c.set {
			t.Setenv("TEMPLATES_DEFAULT_PUBLIC", c.val)
		} else {
			os.Unsetenv("TEMPLATES_DEFAULT_PUBLIC")
		}
		if got := templatesDefaultPublic(); got != c.want {
			t.Errorf("val=%q set=%v: got %v want %v", c.val, c.set, got, c.want)
		}
	}
}
