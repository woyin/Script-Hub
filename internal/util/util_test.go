package util

import (
	"testing"
)

func TestIsTrue(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"1":     true,
		"True":  true,
		"":      false,
		"false": false,
		"0":     false,
		"TRUE":  false, // 仅匹配 "True"（首字母大写）
		"yes":   false,
	}
	for in, want := range cases {
		if got := IsTrue(in); got != want {
			t.Errorf("IsTrue(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGetArgArr(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"single", []string{"single"}},
		{"a+b", []string{"a", "b"}},
		{"a➕b+c", []string{"a+b", "c"}},
		{"a➕b➕c", []string{"a+b+c"}},
		{"+leading", []string{"", "leading"}},
		{"trailing+", []string{"trailing", ""}},
	}
	for _, c := range cases {
		got := GetArgArr(c.in)
		if len(got) != len(c.want) {
			t.Errorf("GetArgArr(%q) len = %d, want %d (got=%v want=%v)", c.in, len(got), len(c.want), got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("GetArgArr(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestParseQueryString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"empty", "", map[string]string{}},
		{"simple", "a=1&b=2", map[string]string{"a": "1", "b": "2"}},
		{"with_question", "path?a=1", map[string]string{"a": "1"}},
		{"bare_param_ignored", "bare&a=1", map[string]string{"a": "1"}},
		{"url_encoded", "name=hello%20world", map[string]string{"name": "hello world"}},
		{"plus_not_space", "v=a+b", map[string]string{"v": "a+b"}},
		{"empty_value", "a=&b=2", map[string]string{"a": "", "b": "2"}},
		{"trailing_amp", "a=1&", map[string]string{"a": "1"}},
		{"chinese", "%E7%B1%BB%E5%9E%8B=qx", map[string]string{"类型": "qx"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseQueryString(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d (got=%v)", len(got), len(c.want), got)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("key %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestParseQueryStringLenient(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"empty", "", map[string]string{}},
		{"simple", "a=1&b=2", map[string]string{"a": "1", "b": "2"}},
		{"bare_param_kept", "bare&a=1", map[string]string{"bare": "", "a": "1"}},
		{"no_decode", "name=a%20b", map[string]string{"name": "a%20b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseQueryStringLenient(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d (got=%v)", len(got), len(c.want), got)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("key %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
