package textutil

import (
	"regexp"
	"strings"
)

var linkRe = regexp.MustCompile(`(?i)(?:(?:https?|ftp|sftp)://|magnet:\?xt=)[^\s<>"'\p{Cc}\p{Z}]+|file://[^\s<>"'\p{Cc}\p{Z}]*\.torrent|/[^\s<>"'\p{Cc}\p{Z}]*\.torrent`)

// ExtractLinks returns the downloadable links in text, de-duplicated, in order of appearance.
func ExtractLinks(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, loc := range linkRe.FindAllStringIndex(text, -1) {
		s := trimTail(text[loc[0]:loc[1]])
		if s == "" {
			continue
		}
		// a bare /path.torrent glued to a word is part of something else
		if s[0] == '/' && loc[0] > 0 && !strings.ContainsRune(" \t\r\n(\"'<[", rune(text[loc[0]-1])) {
			continue
		}
		// /a.torrent.bak is some other file, not a torrent
		if end := loc[0] + len(s); end < len(text) && !strings.HasPrefix(strings.ToLower(s), "http") && strings.HasSuffix(strings.ToLower(s), ".torrent") && glued(text[end:]) {
			continue
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func trimTail(s string) string {
	for s != "" {
		switch last := s[len(s)-1]; {
		case strings.IndexByte(".,]", last) >= 0:
		case last == ')' && !strings.Contains(s, "("):
		default:
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}

// glued reports whether rest continues the word before it, so a match
// ending there was cut short.
func glued(rest string) bool {
	c := rest[0]
	return c == '-' || c == '_' || c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'z' || c == '.' && len(rest) > 1 && rest[1] != ' ' && rest[1] != '\n' && rest[1] != '\t' && rest[1] != '\r'
}
