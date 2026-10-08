package backend

import (
	"reflect"
	"sort"
	"testing"

	"pwdtt/backend"
)

func TestNormalizeExclude(t *testing.T) {
	ok := []struct{ in, kind, out string }{
		{"10.0.0.0/8", "cidr", "10.0.0.0/8"},
		{" 10.1.2.3/8 ", "cidr", "10.0.0.0/8"},
		{"89.169.16.1", "cidr", "89.169.16.1/32"},
		{"meet.example.ru", "domain", "meet.example.ru"},
		{"MEET.Example.RU.", "domain", "meet.example.ru"},
		{"https://meet.example.ru/room/123?x=1", "domain", "meet.example.ru"},
		{"meet.example.ru/room", "domain", "meet.example.ru"},
		{"xn--80ak6aa92e.com", "domain", "xn--80ak6aa92e.com"},
	}
	for _, c := range ok {
		kind, v, err := backend.NormalizeExclude(c.in)
		if err != nil || kind != c.kind || v != c.out {
			t.Errorf("%q → (%q, %q, %v), want (%q, %q)", c.in, kind, v, err, c.kind, c.out)
		}
	}
	bad := []string{"", "  ", "0.0.0.0/0", "10.0.0.0/4", "300.1.1.1", "::1", "2001:db8::/32", "localhost", "meet example.ru", "a;rm -rf /"}
	for _, in := range bad {
		if _, v, err := backend.NormalizeExclude(in); err == nil {
			t.Errorf("%q должно быть ошибкой, получили %q", in, v)
		}
	}
}

func TestValidateExcludesDedup(t *testing.T) {
	in := []backend.ExcludeEntry{
		{Value: "10.0.0.0/8", Enabled: true},
		{Value: "10.5.0.0/8", Enabled: false}, // та же сеть после нормализации
		{Value: "meet.example.ru", Enabled: true},
	}
	out, err := backend.ValidateExcludes(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []backend.ExcludeEntry{{Value: "10.0.0.0/8", Enabled: true}, {Value: "meet.example.ru", Enabled: true}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %v want %v", out, want)
	}
	if _, err := backend.ValidateExcludes([]backend.ExcludeEntry{{Value: "мусор", Enabled: true}}); err == nil {
		t.Error("ожидалась ошибка на мусор")
	}
}

func TestDiffCIDRs(t *testing.T) {
	add, del := backend.DiffCIDRs([]string{"a", "b", "c"}, []string{"b", "c", "d", "e"})
	sort.Strings(add)
	sort.Strings(del)
	if !reflect.DeepEqual(add, []string{"d", "e"}) || !reflect.DeepEqual(del, []string{"a"}) {
		t.Errorf("add=%v del=%v", add, del)
	}
}
