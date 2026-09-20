// Package mimeutil contains the untrusted-content handling for mail bodies:
// HTML sanitisation, text/HTML conversion and transfer/charset decoding.
package mimeutil

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// SanitizeOptions controls how message HTML is rewritten.
type SanitizeOptions struct {
	// AllowRemote keeps http(s) images; when false they are replaced by a
	// placeholder and the original URL is kept in data-mh-src so the client
	// can offer "show remote images".
	AllowRemote bool
	// ResolveCID maps a Content-ID (without the angle brackets) to a URL that
	// serves the inline part. Returning "" drops the image.
	ResolveCID func(cid string) string
}

// SanitizeResult is the sanitized fragment plus facts about what was removed.
type SanitizeResult struct {
	HTML             string
	HasRemoteContent bool
}

var policy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowStandardAttributes()
	p.AllowStandardURLs()
	p.AllowURLSchemes("http", "https", "mailto", "tel", "cid", "data")
	p.AllowDataURIImages()
	p.AllowElements(
		"a", "abbr", "address", "article", "aside", "b", "bdi", "bdo", "big", "blockquote", "br", "caption",
		"center", "cite", "code", "col", "colgroup", "dd", "del", "details", "dfn", "div", "dl", "dt", "em",
		"figcaption", "figure", "font", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "i",
		"img", "ins", "kbd", "li", "main", "mark", "nav", "ol", "p", "pre", "q", "s", "samp", "section",
		"small", "span", "strike", "strong", "sub", "summary", "sup", "table", "tbody", "td", "tfoot", "th",
		"thead", "time", "tr", "tt", "u", "ul", "var", "wbr",
	)
	p.AllowAttrs("href", "name").OnElements("a")
	p.AllowAttrs("src", "alt", "width", "height", "align", "border", "hspace", "vspace").OnElements("img")
	p.AllowAttrs("width", "height", "cellpadding", "cellspacing", "border", "align", "valign", "bgcolor",
		"colspan", "rowspan", "nowrap", "role").OnElements("table", "td", "th", "tr", "tbody", "thead", "tfoot", "col", "colgroup", "caption")
	p.AllowAttrs("color", "face", "size").OnElements("font")
	p.AllowAttrs("align").OnElements("p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "center")
	p.AllowAttrs("type", "start", "reversed").OnElements("ol", "ul", "li")
	p.AllowAttrs("style", "class", "dir", "lang", "title").Globally()
	p.AllowAttrs("open").OnElements("details")
	p.SkipElementsContent("script", "style", "title", "head", "noscript", "template", "iframe", "object", "embed", "svg", "math")
	return p
}()

var (
	cssDangerRe = regexp.MustCompile(`(?i)(expression\s*\(|javascript:|vbscript:|@import|behavior\s*:|-moz-binding|position\s*:\s*fixed)`)
	cssURLRe    = regexp.MustCompile(`(?i)url\s*\(`)
	whitespace  = regexp.MustCompile(`\s+`)
)

// transparent 1x1 GIF used in place of blocked remote images.
const blockedPixel = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

// sanitizeCSS drops dangerous declarations from an inline style; reports
// whether any remote url() references were removed.
func sanitizeCSS(style string) (string, bool) {
	remote := false
	decls := strings.Split(style, ";")
	kept := decls[:0]
	for _, d := range decls {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if cssDangerRe.MatchString(d) {
			continue
		}
		if cssURLRe.MatchString(d) {
			if regexp.MustCompile(`(?i)url\s*\(\s*['"]?\s*https?:`).MatchString(d) {
				remote = true
			}
			continue
		}
		kept = append(kept, d)
	}
	return strings.Join(kept, "; "), remote
}

func attr(n *html.Node, key string) (string, int) {
	for i, a := range n.Attr {
		if a.Key == key {
			return a.Val, i
		}
	}
	return "", -1
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func delAttr(n *html.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != key {
			out = append(out, a)
		}
	}
	n.Attr = out
}

func isRemoteURL(u string) bool {
	l := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "//")
}

// SanitizeHTML returns a safe HTML fragment for rendering inside a sandboxed
// iframe. It is a two-stage process: bluemonday enforces the element and
// attribute whitelist, then a DOM pass rewrites image and link references.
func SanitizeHTML(input string, opts SanitizeOptions) SanitizeResult {
	clean := policy.Sanitize(input)
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(clean), root)
	if err != nil {
		return SanitizeResult{HTML: html.EscapeString(HTMLToText(clean))}
	}
	res := SanitizeResult{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if style, _ := attr(n, "style"); style != "" {
				cleaned, remote := sanitizeCSS(style)
				if remote {
					res.HasRemoteContent = true
				}
				if cleaned == "" {
					delAttr(n, "style")
				} else {
					setAttr(n, "style", cleaned)
				}
			}
			switch n.DataAtom {
			case atom.Img:
				src, _ := attr(n, "src")
				l := strings.ToLower(strings.TrimSpace(src))
				switch {
				case strings.HasPrefix(l, "cid:"):
					url := ""
					if opts.ResolveCID != nil {
						url = opts.ResolveCID(strings.TrimSpace(src[4:]))
					}
					if url == "" {
						delAttr(n, "src")
					} else {
						setAttr(n, "src", url)
					}
				case strings.HasPrefix(l, "data:image/"):
					// inline data images are safe to keep
				case isRemoteURL(src):
					res.HasRemoteContent = true
					if !opts.AllowRemote {
						setAttr(n, "data-mh-src", strings.TrimSpace(src))
						setAttr(n, "src", blockedPixel)
						setAttr(n, "class", strings.TrimSpace(func() string { c, _ := attr(n, "class"); return c }()+" mh-blocked"))
					}
				default:
					delAttr(n, "src")
				}
			case atom.A:
				href, _ := attr(n, "href")
				l := strings.ToLower(strings.TrimSpace(href))
				if href != "" && !(strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:") || strings.HasPrefix(l, "tel:")) {
					delAttr(n, "href")
				}
				if _, i := attr(n, "href"); i >= 0 {
					setAttr(n, "target", "_blank")
					setAttr(n, "rel", "noopener noreferrer nofollow")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	var b strings.Builder
	for _, n := range nodes {
		walk(n)
		html.Render(&b, n)
	}
	res.HTML = b.String()
	return res
}

// SanitizeSignature cleans user-authored signature HTML (remote images are
// allowed since the author chose them).
func SanitizeSignature(input string) string {
	return SanitizeHTML(input, SanitizeOptions{AllowRemote: true}).HTML
}
