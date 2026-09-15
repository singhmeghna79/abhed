// Package jsonschema validates JSON against the subset of JSON Schema that
// structured-output contracts actually use.
//
// It exists so a caller can hand Abhed a schema and get back either a value
// that matches it or a list of exactly what does not — path by path, in words
// a model can act on. The full specification is large and mostly about
// hypermedia; what an agent needs is types, required fields, enums, bounds,
// nesting and composition, and those are what this implements. Anything it
// does not understand is rejected at compile time rather than silently
// ignored, because a constraint that is skipped is a contract that is not.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Schema is a compiled schema.
type Schema struct {
	root *node
	defs map[string]*node
}

type node struct {
	types      []string
	properties map[string]*node
	required   []string
	additional *bool // nil = allowed, false = forbidden
	addlSchema *node
	items      *node
	enum       []json.RawMessage
	constV     *json.RawMessage
	min, max   *float64
	exMin      *float64
	exMax      *float64
	minLen     *int
	maxLen     *int
	pattern    *regexp.Regexp
	patternSrc string
	minItems   *int
	maxItems   *int
	unique     bool
	anyOf      []*node
	oneOf      []*node
	allOf      []*node
	not        *node
	ref        string
	desc       string
}

// Error is one violation, with the JSON pointer to where it happened.
type Error struct {
	Path string
	Msg  string
}

func (e Error) Error() string {
	if e.Path == "" {
		return e.Msg
	}
	return e.Path + ": " + e.Msg
}

// Errors is the full list, rendered one per line — the shape a model can
// read and fix.
type Errors []Error

func (es Errors) Error() string {
	var b strings.Builder
	for i, e := range es {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("- ")
		b.WriteString(e.Error())
	}
	return b.String()
}

var known = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true,
	"items": true, "enum": true, "const": true, "minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "minLength": true,
	"maxLength": true, "pattern": true, "minItems": true, "maxItems": true,
	"uniqueItems": true, "anyOf": true, "oneOf": true, "allOf": true, "not": true,
	"$ref": true, "description": true, "title": true, "$schema": true, "$id": true,
	"definitions": true, "$defs": true, "default": true, "examples": true,
	"nullable": true,
}

// Compile parses a schema. It refuses keywords it would not enforce.
func Compile(raw json.RawMessage) (*Schema, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("schema is not a JSON object: %w", err)
	}
	s := &Schema{defs: map[string]*node{}}
	for _, key := range []string{"definitions", "$defs"} {
		if d, ok := top[key]; ok {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(d, &m); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			for name, sub := range m {
				n, err := compileNode(sub, "#/"+key+"/"+name)
				if err != nil {
					return nil, err
				}
				s.defs["#/"+key+"/"+name] = n
			}
		}
	}
	n, err := compileNode(raw, "#")
	if err != nil {
		return nil, err
	}
	s.root = n
	return s, nil
}

func compileNode(raw json.RawMessage, at string) (*node, error) {
	// `true` / `false` schemas.
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		if b {
			return &node{}, nil
		}
		return &node{not: &node{}}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: schema must be an object", at)
	}
	for k := range m {
		if !known[k] {
			return nil, fmt.Errorf("%s: keyword %q is not supported, so it would not be enforced; remove it", at, k)
		}
	}
	n := &node{}
	get := func(k string, v any) (bool, error) {
		r, ok := m[k]
		if !ok {
			return false, nil
		}
		if err := json.Unmarshal(r, v); err != nil {
			return false, fmt.Errorf("%s/%s: %w", at, k, err)
		}
		return true, nil
	}
	if r, ok := m["type"]; ok {
		var one string
		if json.Unmarshal(r, &one) == nil {
			n.types = []string{one}
		} else if err := json.Unmarshal(r, &n.types); err != nil {
			return nil, fmt.Errorf("%s/type: must be a string or list of strings", at)
		}
		for _, t := range n.types {
			switch t {
			case "object", "array", "string", "number", "integer", "boolean", "null":
			default:
				return nil, fmt.Errorf("%s/type: unknown type %q", at, t)
			}
		}
	}
	var nullable bool
	if _, err := get("nullable", &nullable); err != nil {
		return nil, err
	}
	if nullable && len(n.types) > 0 {
		n.types = append(n.types, "null")
	}
	if r, ok := m["properties"]; ok {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(r, &props); err != nil {
			return nil, fmt.Errorf("%s/properties: %w", at, err)
		}
		n.properties = map[string]*node{}
		for name, sub := range props {
			c, err := compileNode(sub, at+"/properties/"+name)
			if err != nil {
				return nil, err
			}
			n.properties[name] = c
		}
	}
	if _, err := get("required", &n.required); err != nil {
		return nil, err
	}
	if r, ok := m["additionalProperties"]; ok {
		var flag bool
		if json.Unmarshal(r, &flag) == nil {
			n.additional = &flag
		} else {
			c, err := compileNode(r, at+"/additionalProperties")
			if err != nil {
				return nil, err
			}
			n.addlSchema = c
		}
	}
	if r, ok := m["items"]; ok {
		c, err := compileNode(r, at+"/items")
		if err != nil {
			return nil, err
		}
		n.items = c
	}
	if _, err := get("enum", &n.enum); err != nil {
		return nil, err
	}
	if r, ok := m["const"]; ok {
		n.constV = &r
	}
	for k, dst := range map[string]**float64{"minimum": &n.min, "maximum": &n.max,
		"exclusiveMinimum": &n.exMin, "exclusiveMaximum": &n.exMax} {
		var f float64
		if ok, err := get(k, &f); err != nil {
			return nil, err
		} else if ok {
			v := f
			*dst = &v
		}
	}
	for k, dst := range map[string]**int{"minLength": &n.minLen, "maxLength": &n.maxLen,
		"minItems": &n.minItems, "maxItems": &n.maxItems} {
		var i int
		if ok, err := get(k, &i); err != nil {
			return nil, err
		} else if ok {
			v := i
			*dst = &v
		}
	}
	var pat string
	if ok, err := get("pattern", &pat); err != nil {
		return nil, err
	} else if ok {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("%s/pattern: %w", at, err)
		}
		n.pattern, n.patternSrc = re, pat
	}
	if _, err := get("uniqueItems", &n.unique); err != nil {
		return nil, err
	}
	for k, dst := range map[string]*[]*node{"anyOf": &n.anyOf, "oneOf": &n.oneOf, "allOf": &n.allOf} {
		if r, ok := m[k]; ok {
			var subs []json.RawMessage
			if err := json.Unmarshal(r, &subs); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", at, k, err)
			}
			for i, sub := range subs {
				c, err := compileNode(sub, fmt.Sprintf("%s/%s/%d", at, k, i))
				if err != nil {
					return nil, err
				}
				*dst = append(*dst, c)
			}
		}
	}
	if r, ok := m["not"]; ok {
		c, err := compileNode(r, at+"/not")
		if err != nil {
			return nil, err
		}
		n.not = c
	}
	if _, err := get("$ref", &n.ref); err != nil {
		return nil, err
	}
	if _, err := get("description", &n.desc); err != nil {
		return nil, err
	}
	return n, nil
}

// Validate checks a JSON document. A nil return means it matches.
func (s *Schema) Validate(doc json.RawMessage) error {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return Errors{{Path: "", Msg: "not valid JSON: " + err.Error()}}
	}
	var errs Errors
	s.check(s.root, v, "$", &errs, 0)
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func (s *Schema) resolve(n *node) *node {
	for n != nil && n.ref != "" {
		next, ok := s.defs[n.ref]
		if !ok {
			return &node{not: &node{}, desc: "unresolved $ref " + n.ref}
		}
		n = next
	}
	return n
}

func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func (s *Schema) check(n *node, v any, path string, errs *Errors, depth int) {
	if depth > 64 {
		*errs = append(*errs, Error{path, "nesting too deep"})
		return
	}
	n = s.resolve(n)
	if n.not != nil && n.desc != "" && strings.HasPrefix(n.desc, "unresolved") {
		*errs = append(*errs, Error{path, n.desc})
		return
	}

	// A `false` schema (compiled as not:{}) matches nothing.
	if n.not != nil {
		var sub Errors
		s.check(n.not, v, path, &sub, depth+1)
		if len(sub) == 0 {
			*errs = append(*errs, Error{path, "matches a schema it must not"})
			return
		}
	}

	if len(n.types) > 0 {
		got := typeOf(v)
		ok := false
		for _, t := range n.types {
			if t == got || (t == "number" && got == "integer") {
				ok = true
				break
			}
		}
		if !ok {
			*errs = append(*errs, Error{path, fmt.Sprintf("expected %s, got %s",
				strings.Join(n.types, " or "), got)})
			return
		}
	}

	if len(n.enum) > 0 {
		raw, _ := json.Marshal(v)
		found := false
		for _, e := range n.enum {
			if jsonEqual(raw, e) {
				found = true
				break
			}
		}
		if !found {
			opts := make([]string, 0, len(n.enum))
			for _, e := range n.enum {
				opts = append(opts, string(e))
			}
			*errs = append(*errs, Error{path, "must be one of " + strings.Join(opts, ", ")})
		}
	}
	if n.constV != nil {
		raw, _ := json.Marshal(v)
		if !jsonEqual(raw, *n.constV) {
			*errs = append(*errs, Error{path, "must equal " + string(*n.constV)})
		}
	}

	switch x := v.(type) {
	case string:
		if n.minLen != nil && len([]rune(x)) < *n.minLen {
			*errs = append(*errs, Error{path, fmt.Sprintf("shorter than %d characters", *n.minLen)})
		}
		if n.maxLen != nil && len([]rune(x)) > *n.maxLen {
			*errs = append(*errs, Error{path, fmt.Sprintf("longer than %d characters", *n.maxLen)})
		}
		if n.pattern != nil && !n.pattern.MatchString(x) {
			*errs = append(*errs, Error{path, "does not match pattern " + n.patternSrc})
		}
	case float64:
		if n.min != nil && x < *n.min {
			*errs = append(*errs, Error{path, fmt.Sprintf("below minimum %v", *n.min)})
		}
		if n.max != nil && x > *n.max {
			*errs = append(*errs, Error{path, fmt.Sprintf("above maximum %v", *n.max)})
		}
		if n.exMin != nil && x <= *n.exMin {
			*errs = append(*errs, Error{path, fmt.Sprintf("must be greater than %v", *n.exMin)})
		}
		if n.exMax != nil && x >= *n.exMax {
			*errs = append(*errs, Error{path, fmt.Sprintf("must be less than %v", *n.exMax)})
		}
	case []any:
		if n.minItems != nil && len(x) < *n.minItems {
			*errs = append(*errs, Error{path, fmt.Sprintf("fewer than %d items", *n.minItems)})
		}
		if n.maxItems != nil && len(x) > *n.maxItems {
			*errs = append(*errs, Error{path, fmt.Sprintf("more than %d items", *n.maxItems)})
		}
		if n.unique {
			seen := map[string]bool{}
			for i, it := range x {
				raw, _ := json.Marshal(it)
				if seen[string(raw)] {
					*errs = append(*errs, Error{fmt.Sprintf("%s[%d]", path, i), "duplicate item"})
				}
				seen[string(raw)] = true
			}
		}
		if n.items != nil {
			for i, it := range x {
				s.check(n.items, it, fmt.Sprintf("%s[%d]", path, i), errs, depth+1)
			}
		}
	case map[string]any:
		for _, req := range n.required {
			if _, ok := x[req]; !ok {
				*errs = append(*errs, Error{path + "." + req, "is required"})
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sub, ok := n.properties[k]; ok {
				s.check(sub, x[k], path+"."+k, errs, depth+1)
				continue
			}
			if n.additional != nil && !*n.additional {
				*errs = append(*errs, Error{path + "." + k, "is not a known field"})
			} else if n.addlSchema != nil {
				s.check(n.addlSchema, x[k], path+"."+k, errs, depth+1)
			}
		}
	}

	if len(n.allOf) > 0 {
		for i, sub := range n.allOf {
			var se Errors
			s.check(sub, v, path, &se, depth+1)
			for _, e := range se {
				*errs = append(*errs, Error{e.Path, fmt.Sprintf("(allOf %d) %s", i, e.Msg)})
			}
		}
	}
	if len(n.anyOf) > 0 {
		matched := false
		for _, sub := range n.anyOf {
			var se Errors
			s.check(sub, v, path, &se, depth+1)
			if len(se) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			*errs = append(*errs, Error{path, "matches none of the allowed alternatives"})
		}
	}
	if len(n.oneOf) > 0 {
		count := 0
		for _, sub := range n.oneOf {
			var se Errors
			s.check(sub, v, path, &se, depth+1)
			if len(se) == 0 {
				count++
			}
		}
		if count != 1 {
			*errs = append(*errs, Error{path, fmt.Sprintf("must match exactly one alternative, matched %d", count)})
		}
	}
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}
