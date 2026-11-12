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
		if unicode.IsControl(r) || isBidi(r) {
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

// isBidi matches the direction controls (and the BOM) that can disguise an
// extension. ZWJ and ZWNJ stay: emoji and Indic scripts need them.
func isBidi(r rune) bool {
	return r == 0x061C || r == 0xFEFF || r == 0x200E || r == 0x200F ||
		r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069
}
