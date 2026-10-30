// Package apitypes renders TypeScript declarations for the JSON the HTTP API
// returns, straight from the Go response types, so the web client cannot
// drift from the server.
//
// The mapping follows encoding/json: exported fields only, `json:"name"`
// renames, `json:"-"` skips, `omitempty` makes a field optional, embedded
// structs without a tag are flattened, and pointers may be null. A field
// tag `tstype:"..."` overrides the TypeScript type of that field.
package apitypes

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Enum declares the allowed values of a named Go string type.
type Enum struct {
	Type   reflect.Type
	Values []string
}

// Generator collects declarations reachable from the root types.
type Generator struct {
	names map[reflect.Type]string
	decls map[string]string
	enums map[reflect.Type][]string
	err   error
}

func New(enums ...Enum) *Generator {
	g := &Generator{names: map[reflect.Type]string{}, decls: map[string]string{}, enums: map[reflect.Type][]string{}}
	for _, e := range enums {
		g.enums[e.Type] = e.Values
	}
	return g
}

// Rename gives v's type a TypeScript name other than its Go name, for
// types whose Go names collide across packages.
func (g *Generator) Rename(v any, name string) *Generator {
	t := reflect.TypeOf(v)
	for other, used := range g.names {
		if used == name {
			g.fail("TypeScript name %s used by both %s and %s", name, other, t)
		}
	}
	g.names[t] = name
	return g
}

// Add declares v's type (a struct value) and everything it references.
func (g *Generator) Add(values ...any) *Generator {
	for _, v := range values {
		t := reflect.TypeOf(v)
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || t.Name() == "" {
			g.fail("root %v must be a named struct", t)
			continue
		}
		g.typeExpr(t)
	}
	return g
}

func (g *Generator) fail(format string, args ...any) {
	if g.err == nil {
		g.err = fmt.Errorf(format, args...)
	}
}

var (
	timeType    = reflect.TypeOf(time.Time{})
	rawJSONType = reflect.TypeOf(json.RawMessage{})
	marshaler   = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
)

// declare reserves the TypeScript name of a named Go type, refusing two
// different Go types with the same name.
func (g *Generator) declare(t reflect.Type) (string, bool) {
	if name, ok := g.names[t]; ok {
		_, written := g.decls[name]
		return name, !written
	}
	name := t.Name()
	for other := range g.names {
		if g.names[other] == name {
			g.fail("TypeScript name %s used by both %s and %s", name, other.PkgPath(), t.PkgPath())
		}
	}
	g.names[t] = name
	return name, true
}

func (g *Generator) typeExpr(t reflect.Type) string {
	if t == timeType {
		return "string"
	}
	if t == rawJSONType {
		return "unknown"
	}
	if t.Implements(marshaler) || reflect.PointerTo(t).Implements(marshaler) {
		g.fail("%s has a custom MarshalJSON; give the field a tstype tag", t)
		return "unknown"
	}
	if values, ok := g.enums[t]; ok {
		name, isNew := g.declare(t)
		if isNew {
			g.decls[name] = ""
			quoted := make([]string, len(values))
			for i, v := range values {
				quoted[i] = strconv.Quote(v)
			}
			g.decls[name] = "export type " + name + " = " + strings.Join(quoted, " | ") + ";\n"
		}
		return name
	}
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Interface:
		return "unknown"
	case reflect.Pointer:
		return g.typeExpr(t.Elem()) + " | null"
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return "string" // base64
		}
		return arrayOf(g.typeExpr(t.Elem()))
	case reflect.Map:
		if k := t.Key().Kind(); k != reflect.String && (k < reflect.Int || k > reflect.Uint64) {
			g.fail("map key %s is not representable in JSON", t.Key())
		}
		return "Record<string, " + g.typeExpr(t.Elem()) + ">"
	case reflect.Struct:
		if t.Name() == "" {
			return g.object(t, "")
		}
		name, isNew := g.declare(t)
		if isNew {
			g.decls[name] = "" // reserve before recursing (self-references)
			g.decls[name] = "export interface " + name + " " + g.object(t, "") + "\n"
		}
		return name
	}
	g.fail("unsupported type %s", t)
	return "unknown"
}

func arrayOf(elem string) string {
	if strings.ContainsAny(elem, " |") {
		return "(" + elem + ")[]"
	}
	return elem + "[]"
}

type field struct {
	name, expr string
	optional   bool
}

// fields lists t's JSON fields in declaration order, flattening untagged
// embedded structs the way encoding/json does: the shallowest field of a
// name wins, and a name that is ambiguous at that depth is dropped.
func (g *Generator) fields(t reflect.Type) []field {
	type candidate struct {
		field
		depth int
	}
	var all []candidate
	var walk func(t reflect.Type, depth int)
	walk = func(t reflect.Type, depth int) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			ft := f.Type
			if f.Anonymous && name == "" {
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft, depth+1)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			expr := f.Tag.Get("tstype")
			if expr == "" {
				if strings.Contains(","+opts+",", ",string,") {
					expr = "string"
				} else {
					expr = g.typeExpr(ft)
				}
			}
			optional := strings.Contains(","+opts+",", ",omitempty,") || strings.Contains(","+opts+",", ",omitzero,")
			all = append(all, candidate{field{name: name, expr: expr, optional: optional}, depth})
		}
	}
	walk(t, 0)
	shallowest, count := map[string]int{}, map[string]int{}
	for _, c := range all {
		if d, ok := shallowest[c.name]; !ok || c.depth < d {
			shallowest[c.name], count[c.name] = c.depth, 0
		}
		if c.depth == shallowest[c.name] {
			count[c.name]++
		}
	}
	var out []field
	for _, c := range all {
		if c.depth == shallowest[c.name] && count[c.name] == 1 {
			out = append(out, c.field)
		}
	}
	return out
}

func (g *Generator) object(t reflect.Type, indent string) string {
	var b strings.Builder
	b.WriteString("{\n")
	for _, f := range g.fields(t) {
		key := f.name
		if !isIdentifier(key) {
			key = strconv.Quote(key)
		}
		if f.optional {
			key += "?"
		}
		expr := strings.ReplaceAll(f.expr, "\n", "\n"+indent+"  ")
		fmt.Fprintf(&b, "%s  %s: %s;\n", indent, key, expr)
	}
	b.WriteString(indent + "}")
	return b.String()
}

func isIdentifier(s string) bool {
	for i, r := range s {
		if !(r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return s != ""
}

// Source returns the TypeScript file, declarations sorted by name.
func (g *Generator) Source(header string) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	names := make([]string, 0, len(g.decls))
	for name := range g.decls {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(header)
	for _, name := range names {
		b.WriteString("\n")
		b.WriteString(g.decls[name])
	}
	return b.String(), nil
}
