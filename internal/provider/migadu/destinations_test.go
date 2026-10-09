package migadu

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMultiProviderDestinationFormats(t *testing.T) {
	expected := []string{"one@example.org", "two@example.org"}
	for _, input := range []any{expected, "one@example.org,two@example.org", "one@example.org, two@example.org"} {
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		values, err := decodeDestinations(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(values, expected) {
			t.Fatal("标准 JSON 或 CSV 地址没有完整读取")
		}
	}
	for _, input := range []any{[]any{"one@example.org", true}, "one@example.org\ntwo@example.org", map[string]string{"address": "one@example.org"}, "Display Name <one@example.org>"} {
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeDestinations(encoded); err == nil {
			t.Fatal("无效 destinations 没有被拒绝")
		}
	}
}
