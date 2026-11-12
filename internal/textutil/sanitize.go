package textutil

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxName = 255

// SanitizeName makes a server-supplied name safe to use as a single file name.
func SanitizeName(name string, fat bool) string {
	name = strings.Map(func(r rune) rune {
		// Cf covers the bidi overrides that can disguise an extension.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name)
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' })
	keep := parts[:0]
	for _, p := range parts {
		if p != ".." && p != "." {
			keep = append(keep, p)
		}
	}
	name = strings.TrimLeft(strings.Join(keep, "_"), "-.")
	if fat {
		name = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`<>:"|?*`, r) {
				return '_'
			}
			return r
		}, name)
	}
	if len(name) > maxName {
		name = name[:maxName]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	if name == "" {
		return "download"
	}
	return name
}
