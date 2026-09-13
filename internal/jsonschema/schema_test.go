package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"
)

const invoice = `{
  "type": "object",
  "required": ["vendor", "total", "lines"],
  "additionalProperties": false,
  "properties": {
    "vendor":   {"type": "string", "minLength": 1},
    "total":    {"type": "number", "minimum": 0},
    "currency": {"type": "string", "enum": ["USD", "EUR", "INR"]},
    "paid":     {"type": "boolean"},
    "lines": {
      "type": "array", "minItems": 1,
      "items": {"$ref": "#/$defs/line"}
    }
  },
  "$defs": {
    "line": {
      "type": "object", "required": ["sku", "qty"],
      "properties": {
        "sku": {"type": "string", "pattern": "^[A-Z]{2}-[0-9]+$"},
        "qty": {"type": "integer", "exclusiveMinimum": 0}
      }
    }
  }
}`

func TestValidDocumentPasses(t *testing.T) {
	s, err := Compile(json.RawMessage(invoice))
	if err != nil {
		t.Fatal(err)
	}
	doc := `{"vendor":"Acme","total":12.5,"currency":"EUR","lines":[{"sku":"AB-1","qty":2}]}`
	if err := s.Validate(json.RawMessage(doc)); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
}

// Every violation is reported, with a path, in one pass — so a model gets the
// whole list and fixes it once rather than discovering errors one retry at a
// time.
func TestEveryViolationIsReportedWithItsPath(t *testing.T) {
	s, _ := Compile(json.RawMessage(invoice))
	doc := `{"vendor":"","total":-1,"currency":"GBP","extra":1,
	         "lines":[{"sku":"bad","qty":0},{"qty":1.5}]}`
	err := s.Validate(json.RawMessage(doc))
	if err == nil {
		t.Fatal("invalid document accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"$.vendor: shorter than 1",
		"$.total: below minimum 0",
		"$.currency: must be one of",
		"$.extra: is not a known field",
		"$.lines[0].sku: does not match pattern",
		"$.lines[0].qty: must be greater than 0",
		"$.lines[1].sku: is required",
		"$.lines[1].qty: expected integer, got number",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
}

// A keyword this validator would not enforce is refused at compile time. A
// silently skipped constraint is a contract that is not.
func TestUnsupportedKeywordIsRefusedNotIgnored(t *testing.T) {
	_, err := Compile(json.RawMessage(`{"type":"object","dependentRequired":{"a":["b"]}}`))
	if err == nil || !strings.Contains(err.Error(), "dependentRequired") {
		t.Fatalf("unsupported keyword accepted: %v", err)
	}
}

func TestComposition(t *testing.T) {
	s, err := Compile(json.RawMessage(`{
	  "oneOf": [
	    {"type":"object","required":["ok"],"properties":{"ok":{"const":true}}},
	    {"type":"object","required":["error"],"properties":{"error":{"type":"string"}}}
	  ]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(json.RawMessage(`{"ok":true}`)); err != nil {
		t.Errorf("first branch rejected: %v", err)
	}
	if err := s.Validate(json.RawMessage(`{"error":"boom"}`)); err != nil {
		t.Errorf("second branch rejected: %v", err)
	}
	if err := s.Validate(json.RawMessage(`{"ok":true,"error":"x"}`)); err == nil {
		t.Error("document matching both oneOf branches was accepted")
	}
	if err := s.Validate(json.RawMessage(`{}`)); err == nil {
		t.Error("document matching neither branch was accepted")
	}
}

func TestNullableAndTypeLists(t *testing.T) {
	s, _ := Compile(json.RawMessage(`{"type":"object","properties":{
	  "a":{"type":["string","null"]}, "b":{"type":"string","nullable":true}}}`))
	if err := s.Validate(json.RawMessage(`{"a":null,"b":null}`)); err != nil {
		t.Errorf("nulls rejected: %v", err)
	}
	if err := s.Validate(json.RawMessage(`{"a":1}`)); err == nil {
		t.Error("number accepted for string|null")
	}
}

func TestNotJSONIsAnError(t *testing.T) {
	s, _ := Compile(json.RawMessage(`{"type":"object"}`))
	if err := s.Validate(json.RawMessage(`{nope`)); err == nil {
		t.Error("malformed JSON accepted")
	}
}
