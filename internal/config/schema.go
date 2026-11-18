package config

import (
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// Source says which layer a value came from.
type Source int

const (
	SourceDefault Source = iota
	SourceFile
	SourceEnv
	SourceOverride
	SourceFlag
)

func (s Source) String() string {
	return [...]string{"default", "file", "env", "override", "flag"}[s]
}

// Entry is one setting with its current value and where it came from.
type Entry struct {
	Key    string // "downloads.dir"
	Value  any
	Source Source
	Live   bool
	Help   string
	Enum   []string
	Unused bool // nothing reads it yet; it waits for the terminal interface
}

// field describes one leaf setting, built once from the struct tags.
type field struct {
	key      string
	idx      []int // path into Settings; nil for the keys.preset and dynamic map fields
	typ      reflect.Type
	def      string
	live     bool
	help     string
	enum     []string
	min, max *float64
	kind     string
	mapName  string // "keys" or "theme" for map backed fields
	mapKey   string
	unused   bool
}

var (
	schemaOnce sync.Once
	schema     []*field
	byKey      map[string]*field
)

func fields() []*field {
	schemaOnce.Do(func() {
		byKey = map[string]*field{}
		walk(reflect.TypeOf(Settings{}), nil, "", false)
		add(&field{key: "keys.preset", typ: reflect.TypeOf(""), def: "default", live: true,
			enum: []string{"default", "vim"}, help: "Keymap preset.", mapName: "keys", mapKey: "preset", unused: true})
	})
	return schema
}

func add(f *field) {
	schema = append(schema, f)
	byKey[f.key] = f
}

func walk(t reflect.Type, idx []int, prefix string, unused bool) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		name := sf.Tag.Get("toml")
		path := append(append([]int{}, idx...), i)
		unused := unused || sf.Tag.Get("unused") == "true"
		switch {
		case sf.Type.Kind() == reflect.Map:
		case sf.Type.Kind() == reflect.Struct:
			walk(sf.Type, path, name+".", unused)
		default:
			f := &field{key: prefix + name, idx: path, typ: sf.Type, def: sf.Tag.Get("default"),
				live: sf.Tag.Get("live") == "true", help: sf.Tag.Get("help"), kind: sf.Tag.Get("kind"), unused: unused}
			if e := sf.Tag.Get("enum"); e != "" {
				f.enum = strings.Split(e, "|")
			}
			f.min, f.max = num(sf.Tag.Get("min")), num(sf.Tag.Get("max"))
			add(f)
		}
	}
}

func num(s string) *float64 {
	if s == "" {
		return nil
	}
	v, _ := strconv.ParseFloat(s, 64)
	return &v
}

// lookup finds a static field, or builds one for a keys.* / theme.* binding.
func lookup(key string) *field {
	fields()
	if f := byKey[key]; f != nil {
		return f
	}
	if name, k, ok := strings.Cut(key, "."); ok && k != "" {
		switch name {
		case "keys":
			return &field{key: key, typ: reflect.TypeOf([]string{}), live: true, kind: "keys",
				help: "Keys bound to this action.", mapName: "keys", mapKey: k, unused: true}
		case "theme":
			return &field{key: key, typ: reflect.TypeOf(""), live: true, kind: "color",
				help: "Colour for this theme role.", mapName: "theme", mapKey: k, unused: true}
		}
	}
	return nil
}

// Unused reports whether key is a setting nothing reads yet.
func Unused(key string) bool {
	f := lookup(key)
	return f != nil && f.unused
}

// Schema returns every built-in setting with its default value.
func Schema() []Entry {
	s := defaults()
	return entries(s, nil)
}

func entries(s *Settings, src map[string]Source) []Entry {
	var out []Entry
	for _, f := range fields() {
		out = append(out, f.entry(s, src[f.key]))
	}
	return out
}

func (f *field) entry(s *Settings, src Source) Entry {
	return Entry{Key: f.key, Value: f.get(s), Source: src, Live: f.live, Help: f.help, Enum: f.enum, Unused: f.unused}
}

func (f *field) get(s *Settings) any {
	switch f.mapName {
	case "keys":
		if f.mapKey == "preset" {
			return s.Keys["preset"][0]
		}
		return s.Keys[f.mapKey]
	case "theme":
		return s.Theme[f.mapKey]
	}
	return reflect.ValueOf(s).Elem().FieldByIndex(f.idx).Interface()
}

func (f *field) set(s *Settings, v any) {
	switch f.mapName {
	case "keys":
		if f.mapKey == "preset" {
			s.Keys["preset"] = []string{v.(string)}
		} else {
			s.Keys[f.mapKey] = v.([]string)
		}
	case "theme":
		s.Theme[f.mapKey] = v.(string)
	default:
		reflect.ValueOf(s).Elem().FieldByIndex(f.idx).Set(reflect.ValueOf(v))
	}
}

func defaults() *Settings {
	s := &Settings{Keys: map[string][]string{"preset": {"default"}}, Theme: map[string]string{}}
	for _, f := range fields() {
		if f.idx != nil {
			v, err := f.fromString(f.def)
			if err != nil {
				panic("bad default for " + f.key + ": " + err.Error())
			}
			f.set(s, v)
		}
	}
	return s
}
