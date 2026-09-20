package mimeutil

import (
	"io"
	"strings"
	"testing"
)

func TestSanitizeHTMLBlocksDangerousContent(t *testing.T) {
	in := `<html><head><style>body{color:red}</style><script>alert(1)</script></head><body>
<p onclick="x()" style="color:blue;background:url(http://evil/x.png);position:fixed">Hi <a href="javascript:alert(1)">bad</a> <a href="https://ok.example/a?b=1">ok</a></p>
<img src="http://tracker.example/p.gif"><img src="cid:logo1"><img src="data:image/png;base64,AAAA">
<iframe src="https://x"></iframe><form action="/steal"><input name="pw"></form>
</body></html>`
	res := SanitizeHTML(in, SanitizeOptions{ResolveCID: func(cid string) string { return "/parts/" + cid }})
	h := res.HTML
	for _, bad := range []string{"<script", "onclick", "javascript:", "<iframe", "<form", "<input", "position", "url("} {
		if strings.Contains(h, bad) {
			t.Errorf("output still contains %q:\n%s", bad, h)
		}
	}
	for _, good := range []string{`href="https://ok.example/a?b=1"`, `target="_blank"`, `rel="noopener noreferrer nofollow"`, `src="/parts/logo1"`, `data-mh-src="http://tracker.example/p.gif"`, `src="data:image/png;base64,AAAA"`, "color:blue"} {
		if !strings.Contains(h, good) {
			t.Errorf("output missing %q:\n%s", good, h)
		}
	}
	if !res.HasRemoteContent {
		t.Error("remote content should be reported")
	}
	allowed := SanitizeHTML(`<img src="https://img.example/a.png">`, SanitizeOptions{AllowRemote: true})
	if !strings.Contains(allowed.HTML, `src="https://img.example/a.png"`) || strings.Contains(allowed.HTML, "data-mh-src") {
		t.Errorf("remote images should be kept when allowed: %s", allowed.HTML)
	}
}

func TestTextToHTML(t *testing.T) {
	out := TextToHTML("Hello <b>\nsee https://example.com/x?y=1.\n> quoted\n>> deeper")
	for _, want := range []string{"Hello &lt;b&gt;", `<a href="https://example.com/x?y=1"`, `mh-quote-1`, `mh-quote-2`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	txt := HTMLToText(`<div><p>Hello <b>world</b></p><ul><li>one</li><li>two</li></ul><blockquote><p>quoted line</p></blockquote><a href="https://x.example">site</a><script>bad()</script></div>`)
	for _, want := range []string{"Hello world", "- one", "- two", "> quoted line", "site <https://x.example>"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in %q", want, txt)
		}
	}
	if strings.Contains(txt, "bad()") {
		t.Error("script content leaked")
	}
}

func TestDecodeBody(t *testing.T) {
	b64 := "SGVsbG8g\r\nd29ybGQ="
	got, _ := io.ReadAll(DecodeBody(strings.NewReader(b64), "base64", "utf-8"))
	if string(got) != "Hello world" {
		t.Errorf("base64: %q", got)
	}
	qp := "caf=C3=A9"
	got, _ = io.ReadAll(DecodeBody(strings.NewReader(qp), "quoted-printable", "UTF-8"))
	if string(got) != "café" {
		t.Errorf("qp: %q", got)
	}
	gbk := []byte{0xC4, 0xE3, 0xBA, 0xC3} // 你好 in GBK
	got, _ = io.ReadAll(DecodeBody(strings.NewReader(string(gbk)), "8bit", "gbk"))
	if string(got) != "你好" {
		t.Errorf("gbk: %q", got)
	}
}
