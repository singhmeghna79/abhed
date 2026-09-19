package extension

import (
	"strings"
	"testing"
)

// environ layers the extension's own variables on top of Abhed's environment,
// as Config.Env documents. It used to return only `extra`, which silently
// dropped HOME, the locale and PATH — so setting a single variable broke the
// extension process in ways hard to trace back to the cause.
func TestEnvironLayersOnInheritedEnv(t *testing.T) {
	t.Setenv("ABHED_TEST_INHERITED", "yes")
	t.Setenv("ABHED_TEST_OVERRIDE", "old")

	// No extra vars still means inherit unchanged.
	if environ(nil) != nil {
		t.Error("environ(nil) should inherit the environment unchanged")
	}

	got := environ(map[string]string{
		"FOO":                 "bar",
		"ABHED_TEST_OVERRIDE": "new",
	})

	// The last value for a repeated key wins in exec, so an explicit override
	// in extra still takes effect.
	find := func(key string) (string, bool) {
		var val string
		var ok bool
		for _, kv := range got {
			if strings.HasPrefix(kv, key+"=") {
				val, ok = kv[len(key)+1:], true
			}
		}
		return val, ok
	}

	if v, ok := find("ABHED_TEST_INHERITED"); !ok || v != "yes" {
		t.Errorf("inherited var dropped: setting one extension var must not remove the rest (got %q, %v)", v, ok)
	}
	if v, ok := find("FOO"); !ok || v != "bar" {
		t.Errorf("FOO = %q, %v; want bar", v, ok)
	}
	if v, ok := find("ABHED_TEST_OVERRIDE"); !ok || v != "new" {
		t.Errorf("override = %q, %v; extra should win over the inherited value", v, ok)
	}
}
