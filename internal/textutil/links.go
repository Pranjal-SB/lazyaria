package textutil

import (
	"regexp"
	"strings"
)

var linkRe = regexp.MustCompile(`(?i)(?:(?:https?|ftp|sftp)://|magnet:\?xt=)[^\s<>"']+|file://[^\s<>"']*\.torrent|/[^\s<>"']*\.torrent`)

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
		case strings.IndexByte(".,;:!?]", last) >= 0:
		case last == ')' && !strings.Contains(s, "("):
		default:
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}
