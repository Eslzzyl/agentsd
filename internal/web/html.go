package web

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
)

type htmlLink struct {
	URL   string
	Title string
	Class string
}

func parseHTMLDocument(source string) (*xhtml.Node, error) {
	document, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}
	return document, nil
}

func htmlAttribute(node *xhtml.Node, name string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return strings.TrimSpace(attribute.Val)
		}
	}
	return ""
}

func collectNodeText(node *xhtml.Node) string {
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
			return
		}
		if current.Type != xhtml.ElementNode {
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			return
		}
		switch strings.ToLower(current.Data) {
		case "script", "style", "noscript", "template", "svg", "canvas":
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return cleanText(builder.String())
}

func cleanText(text string) string {
	text = html.UnescapeString(text)
	var builder strings.Builder
	space := false
	for _, runeValue := range text {
		if unicode.IsSpace(runeValue) {
			space = true
			continue
		}
		if space && builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		space = false
		builder.WriteRune(runeValue)
	}
	cleaned := strings.TrimSpace(builder.String())
	for _, punctuation := range []string{".", ",", "!", "?", ":", ";", "%", ")", "]", "}"} {
		cleaned = strings.ReplaceAll(cleaned, " "+punctuation, punctuation)
	}
	cleaned = strings.ReplaceAll(cleaned, "( ", "(")
	cleaned = strings.ReplaceAll(cleaned, "[ ", "[")
	cleaned = strings.ReplaceAll(cleaned, "{ ", "{")
	return cleaned
}

func renderHTMLText(source string) (string, error) {
	document, err := parseHTMLDocument(source)
	if err != nil {
		return "", err
	}
	var lines []string
	var current strings.Builder
	flush := func() {
		text := cleanText(current.String())
		if text != "" {
			lines = append(lines, text)
		}
		current.Reset()
	}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			current.WriteString(node.Data)
			current.WriteByte(' ')
			return
		}
		if node.Type != xhtml.ElementNode {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			return
		}
		tag := strings.ToLower(node.Data)
		switch tag {
		case "script", "style", "noscript", "template", "svg", "canvas":
			return
		case "br":
			flush()
			return
		case "hr":
			flush()
			lines = append(lines, "---")
			return
		case "img":
			alt := cleanText(htmlAttribute(node, "alt"))
			if alt != "" {
				current.WriteString("[image: ")
				current.WriteString(alt)
				current.WriteString("] ")
			}
			return
		}
		block := isHTMLBlock(tag)
		if block {
			flush()
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if block {
			flush()
		}
	}
	walk(document)
	flush()
	return strings.Join(compactLines(lines), "\n\n"), nil
}

func isHTMLBlock(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "dd", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hgroup", "li", "main", "nav", "ol", "p", "pre", "section", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func compactLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = cleanText(line)
		if line == "" {
			continue
		}
		if len(result) > 0 && result[len(result)-1] == line {
			continue
		}
		result = append(result, line)
	}
	return result
}

func extractHTMLLinks(source, baseURL string) ([]htmlLink, error) {
	document, err := parseHTMLDocument(source)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	links := make([]htmlLink, 0, 32)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "a") {
			raw := htmlAttribute(node, "href")
			if raw != "" {
				if resolved := resolveHTTPURL(base, raw); resolved != "" {
					links = append(links, htmlLink{
						URL:   resolved,
						Title: collectNodeText(node),
						Class: htmlAttribute(node, "class"),
					})
				}
			}
		}
		if node.Type == xhtml.ElementNode {
			tag := strings.ToLower(node.Data)
			if tag == "script" || tag == "style" || tag == "noscript" || tag == "template" {
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return links, nil
}

func resolveHTTPURL(base *url.URL, raw string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(strings.ToLower(raw), "javascript:") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(parsed)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(resolved.Hostname()), "google.com") && resolved.Path == "/url" {
		target := resolved.Query().Get("q")
		if target == "" {
			target = resolved.Query().Get("url")
		}
		if target == "" {
			return ""
		}
		redirected, err := url.Parse(target)
		if err != nil || redirected.Host == "" || (redirected.Scheme != "http" && redirected.Scheme != "https") {
			return ""
		}
		resolved = redirected
	}
	resolved.Fragment = ""
	return resolved.String()
}

func uniqueSources(sources []SearchSource, limit int) []SearchSource {
	seen := make(map[string]struct{}, len(sources))
	result := make([]SearchSource, 0, minInt(limit, len(sources)))
	for _, source := range sources {
		source.URL = strings.TrimSpace(source.URL)
		if source.URL == "" {
			continue
		}
		if _, exists := seen[source.URL]; exists {
			continue
		}
		seen[source.URL] = struct{}{}
		if source.Title == "" {
			source.Title = source.URL
		}
		result = append(result, source)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func challengePage(body string) bool {
	lower := strings.ToLower(body)
	markers := []string{"captcha", "unusual traffic", "robot check", "verify you are human", "access denied"}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
