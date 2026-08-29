package analyze_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ericsuh/layerlens/internal/analyze"
)

func TestSanitizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		// Rejections. A traversal cannot write anywhere (we never
		// extract), but it must never be silently rewritten into a
		// plausible-looking path either.
		{name: "parent_traversal", input: "../etc/passwd"},
		{name: "interior_traversal_escapes", input: "a/../../b"},
		{name: "traversal_to_root", input: "a/.."},
		{name: "bare_dotdot", input: ".."},
		{name: "absolute_traversal", input: "/../etc/shadow"},
		{name: "embedded_nul", input: "etc/pass\x00wd"},
		{name: "empty", input: ""},
		{name: "current_dir", input: "."},
		{name: "current_dir_slash", input: "./"},
		{name: "root_only", input: "/"},

		// Normalizations.
		{name: "dot_prefix", input: "./foo", want: "/foo", ok: true},
		{name: "absolute", input: "/foo", want: "/foo", ok: true},
		{name: "double_separator", input: "foo//bar", want: "/foo/bar", ok: true},
		{name: "trailing_separator", input: "foo/bar/", want: "/foo/bar", ok: true},
		{name: "dir_with_dot_prefix", input: "./usr/lib/", want: "/usr/lib", ok: true},
		{name: "interior_dot", input: "usr/./lib", want: "/usr/lib", ok: true},
		{name: "interior_traversal_contained", input: "usr/lib/../bin/sh", want: "/usr/bin/sh", ok: true},
		{name: "deep_normal_path", input: "usr/share/doc/pkg/README.md", want: "/usr/share/doc/pkg/README.md", ok: true},
		{name: "plain", input: "etc/hostname", want: "/etc/hostname", ok: true},
		{name: "hidden_file", input: ".dockerenv", want: "/.dockerenv", ok: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := analyze.SanitizePath(tc.input)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsRootName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{".", "./", "/", "//", "./././", ""} {
		assert.True(t, analyze.IsRootName(name), "%q should be the archive root", name)
	}
	for _, name := range []string{"etc", "./etc", "/etc/", "..", "../"} {
		assert.False(t, analyze.IsRootName(name), "%q should not be the archive root", name)
	}
}
