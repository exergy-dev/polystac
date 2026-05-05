package cql2

import "testing"

func TestParseTextSimpleComparison(t *testing.T) {
	e, err := Parse([]byte(`eo:cloud_cover < 10`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	op, ok := e.(*Op)
	if !ok {
		t.Fatalf("want *Op, got %T", e)
	}
	if op.Op != OpLt {
		t.Errorf("operator: got %q want %q", op.Op, OpLt)
	}
	if len(op.Args) != 2 {
		t.Fatalf("args: %d", len(op.Args))
	}
	if _, ok := op.Args[0].(*PropertyRef); !ok {
		t.Errorf("arg0 type: %T", op.Args[0])
	}
}

func TestParseJSONLogical(t *testing.T) {
	src := []byte(`{"op":"and","args":[{"op":"=","args":[{"property":"id"},"x"]},{"op":">","args":[{"property":"n"},1]}]}`)
	e, err := Parse(src)
	if err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if op, ok := e.(*Op); !ok || op.Op != OpAnd {
		t.Fatalf("want and-Op, got %T %+v", e, e)
	}
}

func TestParseError(t *testing.T) {
	_, err := Parse([]byte(`!!!`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsParseError(err) {
		t.Errorf("want *ParseError, got %T: %v", err, err)
	}
}
