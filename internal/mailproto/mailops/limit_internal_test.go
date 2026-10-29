package mailops

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// #5: the read-back limit fails instead of truncating, whichever copy path
// io.Copy takes, and a message of exactly the limit still fits.
func TestLimitedWriterNeverTruncates(t *testing.T) {
	for _, size := range []int{10, 11, 64 << 10} {
		var buf bytes.Buffer
		w := &limitedWriter{w: &buf, n: 10}
		_, err := io.Copy(w, strings.NewReader(strings.Repeat("a", size)))
		if size == 10 {
			if err != nil || buf.Len() != 10 {
				t.Fatalf("恰好等于上限的内容失败：%v，长度 %d", err, buf.Len())
			}
			continue
		}
		if !errors.Is(err, errLimit) {
			t.Fatalf("%d 字节的内容没有返回 errLimit：%v", size, err)
		}
	}
	var buf bytes.Buffer
	w := &limitedWriter{w: &buf, n: 10}
	_, err := io.Copy(w, io.MultiReader(strings.NewReader("aaaaaaaa"), strings.NewReader("bbbbbbbb")))
	if !errors.Is(err, errLimit) {
		t.Fatalf("分段写入超限没有返回 errLimit：%v", err)
	}
}
