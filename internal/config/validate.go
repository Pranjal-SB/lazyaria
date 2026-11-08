package config

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// fromString converts raw text (env, flag, override, CLI) to the field type.
func (f *field) fromString(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	switch f.typ.Kind() {
	case reflect.String:
		return unquote(raw), nil
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("expected true or false")
		}
		return b, nil
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("expected a whole number")
		}
		return n, nil
	case reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("expected a number")
		}
		return n, nil
	}
	return parseList(raw)
}

// unquote lets a pasted TOML string ("foo") work as well as a bare word.
func unquote(raw string) string {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return raw[1 : len(raw)-1]
	}
	return raw
}

// parseList takes "a,b" or a TOML array literal like ["a","b"].
func parseList(raw string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	if strings.HasPrefix(raw, "[") {
		var w struct{ V []string }
		if _, err := toml.Decode("v = "+raw, &w); err != nil {
			return nil, fmt.Errorf("expected a list of strings")
		}
		if w.V == nil {
			w.V = []string{}
		}
		return w.V, nil
	}
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts, nil
}

// fromTOML converts a decoded TOML value, checking its type strictly.
func (f *field) fromTOML(v any) (any, error) {
	bad := func(want string) (any, error) { return nil, fmt.Errorf("expected %s, got %T", want, v) }
	switch f.typ.Kind() {
	case reflect.String:
		if n, ok := v.(int64); ok && f.kind == "color" {
			return strconv.FormatInt(n, 10), nil
		}
		s, ok := v.(string)
		if !ok {
			return bad("a string")
		}
		return s, nil
	case reflect.Bool:
		b, ok := v.(bool)
		if !ok {
			return bad("true or false")
		}
		return b, nil
	case reflect.Int:
		n, ok := v.(int64)
		if !ok {
			return bad("a whole number")
		}
		return int(n), nil
	case reflect.Float64:
		switch n := v.(type) {
		case float64:
			return n, nil
		case int64:
			return float64(n), nil
		}
		return bad("a number")
	}
	arr, ok := v.([]any)
	if !ok {
		return bad("a list")
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return bad("a list of strings")
		}
		out = append(out, s)
	}
	return out, nil
}

// check validates a typed value. The message omits the key; callers add it.
func (f *field) check(v any) error {
	for _, s := range asList(v) {
		if len(f.enum) > 0 && !contains(f.enum, s) {
			return fmt.Errorf("invalid value %q, allowed %s", s, strings.Join(f.enum, "|"))
		}
		if err := f.checkKind(s); err != nil {
			return err
		}
	}
	var n float64
	switch x := v.(type) {
	case int:
		n = float64(x)
	case float64:
		n = x
	default:
		return nil
	}
	if (f.min != nil && n < *f.min) || (f.max != nil && n > *f.max) {
		return fmt.Errorf("invalid value %v, %s", v, f.rangeText())
	}
	return nil
}

func (f *field) rangeText() string {
	switch {
	case f.min != nil && f.max != nil:
		return fmt.Sprintf("allowed %v to %v", *f.min, *f.max)
	case f.min != nil:
		return fmt.Sprintf("allowed %v or more", *f.min)
	}
	return fmt.Sprintf("allowed %v or less", *f.max)
}

func (f *field) checkKind(s string) error {
	var err error
	switch f.kind {
	case "size":
		_, err = ParseSize(s)
	case "duration":
		_, err = ParseDuration(s)
	case "extra_args":
		_, _, err = ParseExtraArg(s)
	case "keys":
		if s == "" {
			err = fmt.Errorf("key names must not be empty")
		}
	case "color":
		if n, e := strconv.Atoi(s); s != "none" && s != "reverse" && !hexColor.MatchString(s) && (e != nil || n < 0 || n > 15) {
			err = fmt.Errorf("invalid colour %q, allowed 0-15, #rrggbb, none or reverse", s)
		}
	}
	return err
}

var extraArgName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Options lazyaria sets itself: the log, the process wiring, and session
// files (the DB is the source of truth). daemon=true would fork away an
// aria2c that holds our secret where the supervisor cannot reap it.
var ownedOptions = map[string]bool{
	"log": true, "log-level": true, "conf-path": true, "stop-with-process": true,
	"daemon": true, "input-file": true, "save-session": true,
}

// ParseExtraArg splits one engine.aria2_extra_args entry, "--name=value" or a
// bare "--flag" (value ""), and rejects anything that could touch the RPC
// endpoint or options lazyaria owns. aria2's conf parser trims whitespace
// around names, so " rpc-secret" must count as rpc-secret.
func ParseExtraArg(arg string) (name, value string, err error) {
	if strings.ContainsAny(arg, "\r\n") {
		return "", "", fmt.Errorf("aria2 option %q contains a line break; remove it", arg)
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(arg), "--")
	if !ok {
		return "", "", fmt.Errorf("aria2 option %q must use the long form --name=value", arg)
	}
	name, value, _ = strings.Cut(rest, "=")
	name = strings.TrimSpace(name)
	switch {
	case rpcName(strings.ToLower(name)):
		return "", "", fmt.Errorf("rpc-* options are not allowed (%s); the RPC endpoint is managed by lazyaria", arg)
	case !extraArgName.MatchString(name):
		return "", "", fmt.Errorf("aria2 option %q has an invalid name (use --name=value with exactly two dashes; the name is lowercase letters, digits and -)", arg)
	case ownedOptions[name]:
		return "", "", fmt.Errorf("aria2 option %s is managed by lazyaria and cannot be overridden (%s)", name, arg)
	}
	return name, value, nil
}

// rpcName also catches getopt_long abbreviations like enable-rp.
func rpcName(name string) bool {
	return strings.HasPrefix(name, "rpc") ||
		(len(name) >= 2 && strings.HasPrefix("rpc", name)) ||
		(len(name) >= len("enable-r") && strings.HasPrefix("enable-rpc", name))
}

// fail builds the user facing error: key, optional line, reason and default.
func (f *field) fail(where string, line int, err error) error {
	loc := f.key
	if line > 0 {
		loc += fmt.Sprintf(" (line %d)", line)
	}
	if where != "" {
		loc += " from " + where
	}
	if f.mapName != "" && f.mapKey != "preset" {
		return fmt.Errorf("%s: %w", loc, err)
	}
	return fmt.Errorf("%s: %w; default %q", loc, err, f.def)
}

func asList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []string:
		return x
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, e := range l {
		if e == s {
			return true
		}
	}
	return false
}
