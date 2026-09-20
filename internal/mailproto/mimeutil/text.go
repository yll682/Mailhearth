package mimeutil

import (
	"encoding/base64"
	"io"
	"mime/quotedprintable"
	"regexp"
	"strings"

	"github.com/emersion/go-message/charset"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	urlRe        = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"']+[^\s<>"'.,;:!?)\]]`)
	multiBlankRe = regexp.MustCompile(`\n{3,}`)
)

// TextToHTML renders a text/plain body as an HTML fragment: escaped,
// linkified, with quoted lines marked so the client can style them.
func TextToHTML(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var b strings.Builder
	b.WriteString(`<div class="mh-text">`)
	for _, line := range strings.Split(text, "\n") {
		depth := 0
		trimmed := line
		for strings.HasPrefix(trimmed, ">") {
			depth++
			trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, ">"), " ")
		}
		if depth > 0 {
			b.WriteString(`<div class="mh-quote mh-quote-` + itoa(min(depth, 4)) + `">`)
		} else {
			b.WriteString(`<div>`)
		}
		b.WriteString(linkify(trimmed))
		if strings.TrimSpace(trimmed) == "" {
			b.WriteString("<br>")
		}
		b.WriteString("</div>")
	}
	b.WriteString("</div>")
	return b.String()
}

func itoa(i int) string {
	return string(rune('0' + i))
}

func linkify(line string) string {
	var b strings.Builder
	last := 0
	for _, m := range urlRe.FindAllStringIndex(line, -1) {
		b.WriteString(html.EscapeString(line[last:m[0]]))
		raw := line[m[0]:m[1]]
		href := raw
		if strings.HasPrefix(strings.ToLower(raw), "www.") {
			href = "http://" + raw
		}
		b.WriteString(`<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer nofollow">` + html.EscapeString(raw) + `</a>`)
		last = m[1]
	}
	b.WriteString(html.EscapeString(line[last:]))
	return b.String()
}

// HTMLToText converts HTML to readable plain text (used for quoting and for
// the text/plain alternative of composed messages).
func HTMLToText(input string) string {
	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return input
	}
	var b strings.Builder
	renderText(&b, doc, false)
	out := multiBlankRe.ReplaceAllString(b.String(), "\n\n")
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func renderText(b *strings.Builder, n *html.Node, pre bool) {
	switch n.Type {
	case html.TextNode:
		if pre {
			b.WriteString(n.Data)
		} else {
			b.WriteString(collapseSpace(n.Data))
		}
		return
	case html.ElementNode:
		switch n.DataAtom {
		case atom.Script, atom.Style, atom.Head, atom.Title, atom.Noscript, atom.Template:
			return
		case atom.Br:
			b.WriteString("\n")
			return
		case atom.Hr:
			b.WriteString("\n----\n")
			return
		case atom.Img:
			if alt, _ := attr(n, "alt"); alt != "" {
				b.WriteString("[" + alt + "]")
			}
			return
		case atom.Blockquote:
			var inner strings.Builder
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				renderText(&inner, c, pre)
			}
			quoted := strings.Split(strings.Trim(inner.String(), "\n"), "\n")
			for i, q := range quoted {
				quoted[i] = "> " + q
			}
			b.WriteString("\n" + strings.Join(quoted, "\n") + "\n")
			return
		}
	}
	block := false
	if n.Type == html.ElementNode {
		switch n.DataAtom {
		case atom.P, atom.Div, atom.Li, atom.Tr, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
			atom.Pre, atom.Table, atom.Ul, atom.Ol, atom.Section, atom.Article, atom.Header, atom.Footer, atom.Dd, atom.Dt:
			block = true
		}
	}
	if block {
		b.WriteString("\n")
	}
	if n.Type == html.ElementNode && n.DataAtom == atom.Li {
		b.WriteString("- ")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderText(b, c, pre || (n.Type == html.ElementNode && n.DataAtom == atom.Pre))
	}
	if n.Type == html.ElementNode && n.DataAtom == atom.A {
		if href, _ := attr(n, "href"); href != "" && !strings.HasPrefix(strings.ToLower(href), "mailto:") {
			text := strings.TrimSpace(nodeText(n))
			if text != href && text != "" && !strings.Contains(text, href) {
				b.WriteString(" <" + href + ">")
			}
		}
	}
	if n.Type == html.ElementNode && (n.DataAtom == atom.Td || n.DataAtom == atom.Th) {
		b.WriteString("\t")
	}
	if block {
		b.WriteString("\n")
	}
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func collapseSpace(s string) string {
	return whitespace.ReplaceAllString(s, " ")
}

// DecodeBody wraps r with the transfer decoding and charset conversion
// described by a MIME part. Unknown charsets fall back to raw bytes.
func DecodeBody(r io.Reader, transferEncoding, cs string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(transferEncoding)) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, &b64Cleaner{r: r})
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	cs = strings.ToLower(strings.TrimSpace(cs))
	if cs != "" && cs != "utf-8" && cs != "us-ascii" && cs != "ascii" {
		if cr, err := charset.Reader(cs, r); err == nil {
			r = cr
		}
	}
	return r
}

// b64Cleaner strips whitespace so lenient base64 bodies decode.
type b64Cleaner struct {
	r io.Reader
}

func (c *b64Cleaner) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	w := 0
	for i := 0; i < n; i++ {
		switch p[i] {
		case '\r', '\n', ' ', '\t':
			continue
		}
		p[w] = p[i]
		w++
	}
	if w == 0 && n > 0 && err == nil {
		return c.Read(p)
	}
	return w, err
}
