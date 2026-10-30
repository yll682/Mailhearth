package apitypes

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type Kind string

type Inner struct {
	Value int `json:"value"`
}

type base struct {
	ID     int64  `json:"id"`
	Shadow string `json:"shadow"`
	//lint:ignore U1000 checks that unexported fields are skipped
	private string
}

type Sample struct {
	base
	Shadow   int              `json:"shadow"`
	Name     string           `json:"name"`
	Note     string           `json:"note,omitempty"`
	Skipped  string           `json:"-"`
	Parent   *Inner           `json:"parent"`
	Children []Inner          `json:"children"`
	Ptrs     []*Inner         `json:"ptrs"`
	ByKey    map[string]Inner `json:"byKey"`
	Count    int64            `json:"count,string"`
	At       time.Time        `json:"at"`
	Raw      json.RawMessage  `json:"raw"`
	Any      any              `json:"any"`
	Bytes    []byte           `json:"bytes"`
	Kind     Kind             `json:"kind"`
	Custom   map[string]any   `json:"custom" tstype:"{ a?: number }"`
	Nested   struct{ X bool } `json:"nested"`
	Untagged string
	Self     *Sample `json:"self,omitempty"`
}

func TestGenerate(t *testing.T) {
	src, err := New(Enum{Type: reflect.TypeOf(Kind("")), Values: []string{"a", "b"}}).Add(Sample{}).Source("// header\n")
	if err != nil {
		t.Fatal(err)
	}
	want := `// header

export interface Inner {
  value: number;
}

export type Kind = "a" | "b";

export interface Sample {
  id: number;
  shadow: number;
  name: string;
  note?: string;
  parent: Inner | null;
  children: Inner[];
  ptrs: (Inner | null)[];
  byKey: Record<string, Inner>;
  count: string;
  at: string;
  raw: unknown;
  any: unknown;
  bytes: string;
  kind: Kind;
  custom: { a?: number };
  nested: {
    X: boolean;
  };
  Untagged: string;
  self?: Sample | null;
}
`
	if src != want {
		t.Fatalf("generated:\n%s\nwant:\n%s", src, want)
	}
}

type marshals struct{}

func (marshals) MarshalJSON() ([]byte, error) { return []byte(`1`), nil }

type withMarshaler struct {
	M marshals `json:"m"`
}

type other struct{ Inner }

func TestGenerateRejectsUnrepresentable(t *testing.T) {
	if _, err := New().Add(withMarshaler{}).Source(""); err == nil || !strings.Contains(err.Error(), "MarshalJSON") {
		t.Fatalf("custom marshaler accepted: %v", err)
	}
	type Inner struct{ Y int }
	if _, err := New().Add(Sample{}, Inner{}).Source(""); err == nil || !strings.Contains(err.Error(), "used by both") {
		t.Fatalf("name collision accepted: %v", err)
	}
	if _, err := New().Add(Inner{}).Rename(other{}, "Inner").Source(""); err == nil {
		t.Fatal("rename onto a used name accepted")
	}
}
