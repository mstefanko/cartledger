package enrichment

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// VisibleText extracts readable page text while skipping script/style/template
// content and comments. It intentionally returns text only; callers should
// store derived suggestions and short evidence snippets, not full HTML.
func VisibleText(raw []byte) string {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return string(raw)
	}
	var parts []string
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, skip bool) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "template", "noscript", "svg", "canvas":
				skip = true
			case "title":
				skip = false
			}
		}
		if !skip && n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				parts = append(parts, text)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip)
		}
	}
	walk(doc, false)
	return NormalizeText(strings.Join(parts, "\n"))
}

func NormalizeText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	last := ""
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" || line == last {
			continue
		}
		out = append(out, line)
		last = line
	}
	return strings.Join(out, "\n")
}
