package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"uploadserver/internal"

	"golang.org/x/net/html"
)

//go:embed static/admin.gohtml
var adminHTML string

//go:embed static/login.gohtml
var loginHTML string

//go:embed static/dashboard.gohtml
var dashboardHTML string

//go:embed static/uploads.gohtml
var uploadsHTML string

//go:embed static/login.css static/login.js static/admin.css static/admin.js static/uploads.css static/uploads.js
var staticFS embed.FS

// uploadsPageData is the template data for the per-token uploads page.
type uploadsPageData struct {
	Token           internal.TokenRecord
	Uploads         []internal.UploadEntry // current page slice
	BaseURL         string
	CSRF            string
	Page            int // 1-indexed current page
	TotalPages      int
	TotalFiles      int // count after filtering (or total when no query)
	TotalUnfiltered int // original count before search filter; 0 when no query
	PerPage         int
	PageStart       int // 1-indexed first file on this page
	PageEnd         int // 1-indexed last file on this page
	Query           string
}

// adminPageData is the template data for the admin page.
type adminPageData struct {
	LoggedIn     bool
	IsAdmin      bool
	IsRoot       bool
	CurrentToken *internal.TokenRecord
	Tokens       []internal.TokenRecord
	Count        int
	Global       internal.Limits // server-wide default quota
	Error        string
	Secret       *newTokenSecret // non-nil when a token was just created
	CSRF         string
}

// newTokenSecret holds the one-time secret displayed after creating a token.
type newTokenSecret struct {
	ID     string
	Role   string
	Secret string
}

// uploadsTmpl is the parsed uploads template
var uploadsTmpl = template.Must(template.New("uploads").Funcs(template.FuncMap{
	"fmtDate": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("Jan 2, 2006 3:04 PM")
	},
	"humanBytes": internal.FormatSize,
	"totalSize": func(entries []internal.UploadEntry) string {
		var total int64
		for _, e := range entries {
			total += e.Size
		}
		return internal.FormatSize(total)
	},
	"totalSizeAll": func(bytes int64) string {
		return internal.FormatSize(bytes)
	},
	"fileIcon": func(name string) string {
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".bmp", ".ico", ".heic", ".svg":
			return "\U0001F5BC" // framed picture
		case ".mp4", ".webm", ".mov":
			return "\U0001F3AC" // clapper board
		case ".mp3", ".flac":
			return "\U0001F3B5" // musical note
		case ".zip", ".rar", ".7z", ".gz":
			return "\U0001F4E6" // package
		case ".pdf":
			return "\U0001F4D1" // bookmark tabs
		case ".exe", ".jar", ".so":
			return "\u2699" // gear
		case ".txt", ".html", ".mhtml", ".css", ".json", ".yaml", ".yml", ".csv", ".conf", ".sh":
			return "\U0001F4C4" // page facing up
		default:
			return "\U0001F4CE" // paperclip
		}
	},
	"fileURL": func(baseURL, name string) string {
		if baseURL != "" {
			return baseURL + "/" + name
		}
		return "/" + name
	},
	"fileExt": func(name string) string {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		if ext == "" {
			return "FILE"
		}
		return strings.ToUpper(ext)
	},
	"stripExt": func(name string) string {
		ext := filepath.Ext(name)
		if ext == "" {
			return name
		}
		return strings.TrimSuffix(name, ext)
	},
	"isImage": func(name string) bool {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".bmp", ".svg":
			return true
		}
		return false
	},
	"extClass": func(name string) string {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".bmp", ".svg":
			return "ext-img"
		case ".mp4", ".webm", ".mov", ".avi", ".mkv":
			return "ext-vid"
		case ".mp3", ".flac", ".wav", ".ogg", ".m4a":
			return "ext-aud"
		case ".zip", ".rar", ".7z", ".gz", ".tar":
			return "ext-arc"
		case ".pdf", ".doc", ".docx", ".txt", ".md", ".json":
			return "ext-doc"
		default:
			return ""
		}
	},
	// pageRange generates the page numbers to show in the paginator:
	// always page 1 and the last page, plus a window around the current page.
	"pageRange": func(cur, max int) []int {
		if max <= 1 {
			return []int{1}
		}
		delta := 0
		seen := map[int]bool{}
		var pages []int
		add := func(p int) {
			if p >= 1 && p <= max && !seen[p] {
				seen[p] = true
				pages = append(pages, p)
			}
		}
		add(1)
		for i := cur - delta; i <= cur+delta; i++ {
			add(i)
		}
		add(max)
		return pages
	},
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
	"pageURL": func(page int, query string) template.URL {
		s := fmt.Sprintf("?page=%d", page)
		if query != "" {
			s += "&q=" + url.QueryEscape(query)
		}
		return template.URL(s)
	},
}).Parse(uploadsHTML))

// adminTmpl is the parsed admin template
var adminTmpl = template.Must(template.New("admin").Funcs(template.FuncMap{
	"fmtDate": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("Jan 2, 2006 3:04 PM")
	},
	"humanBytes": internal.FormatSize,
	"comma":      internal.Comma,
	"effective":  internal.EffectiveLimits,
	"summary":    internal.SummarizeLimits,
}).Parse(adminHTML))

// init initializes the admin template with the login and dashboard HTML
func init() {
	template.Must(adminTmpl.New("login").Parse(loginHTML))
	template.Must(adminTmpl.New("dashboard").Parse(dashboardHTML))
}

// inlineFormattingElements is a map of HTML tags that are inline formatting elements
var inlineFormattingElements = map[string]bool{
	"a":      true,
	"span":   true,
	"strong": true,
	"em":     true,
	"code":   true,
	"i":      true,
	"b":      true,
	"u":      true,
	"kbd":    true,
	"small":  true,
	"sub":    true,
	"sup":    true,
	"abbr":   true,
	"cite":   true,
	"time":   true,
	"dfn":    true,
	"mark":   true,
	"q":      true,
	"samp":   true,
}

// isStandaloneTag returns true if the given node is a standalone tag (i.e. not an inline formatting element)
func isStandaloneTag(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	return !inlineFormattingElements[n.Data]
}

// shouldStructure returns true if the given node should be structured (i.e. it has children or is a standalone tag)
func shouldStructure(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}

	// Always structure these tags if they contain any children
	switch n.Data {
	case "div", "form", "fieldset", "dialog", "table", "tbody", "thead", "tr", "html", "body", "head", "details", "summary", "ul", "ol", "li", "select", "label":
		return n.FirstChild != nil
	}

	// For other block tags, check if they have structured children or standalone elements
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if shouldStructure(c) || isStandaloneTag(c) {
				return true
			}
		}
	}

	return false
}

// isSelfClosing returns true if the given tag is a self-closing tag
func isSelfClosing(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

// renderNormalizedInline renders the given node as inline content, normalizing whitespace
func renderNormalizedInline(w io.Writer, n *html.Node, isFirst, isLast bool) {
	if n.Type == html.TextNode {
		parentTag := ""
		if n.Parent != nil {
			parentTag = n.Parent.Data
		}
		if parentTag == "pre" || parentTag == "textarea" {
			io.WriteString(w, n.Data)
			return
		}

		text := n.Data
		text = strings.ReplaceAll(text, "\t", " ")
		text = strings.ReplaceAll(text, "\n", " ")
		text = strings.ReplaceAll(text, "\r", " ")
		for strings.Contains(text, "  ") {
			text = strings.ReplaceAll(text, "  ", " ")
		}
		if isFirst {
			text = strings.TrimLeft(text, " ")
		}
		if isLast {
			text = strings.TrimRight(text, " ")
		}
		io.WriteString(w, html.EscapeString(text))
		return
	}

	if n.Type == html.CommentNode {
		io.WriteString(w, "<!--")
		io.WriteString(w, n.Data)
		io.WriteString(w, "-->")
		return
	}

	if n.Type == html.ElementNode {
		io.WriteString(w, "<")
		io.WriteString(w, n.Data)
		for _, attr := range n.Attr {
			io.WriteString(w, " ")
			if attr.Namespace != "" {
				io.WriteString(w, attr.Namespace)
				io.WriteString(w, ":")
			}
			io.WriteString(w, attr.Key)
			io.WriteString(w, `="`)
			io.WriteString(w, html.EscapeString(attr.Val))
			io.WriteString(w, `"`)
		}
		if isSelfClosing(n.Data) {
			io.WriteString(w, " />")
			return
		}
		io.WriteString(w, ">")
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderNormalizedInline(w, c, false, false)
		}
		io.WriteString(w, "</")
		io.WriteString(w, n.Data)
		io.WriteString(w, ">")
	}
}

// flushInline flushes the inline nodes to the output writer
func flushInline(w io.Writer, nodes []*html.Node, depth int) {
	if len(nodes) == 0 {
		return
	}
	start := 0
	for start < len(nodes) {
		if nodes[start].Type == html.TextNode && strings.TrimSpace(nodes[start].Data) == "" {
			start++
		} else {
			break
		}
	}
	end := len(nodes) - 1
	for end >= start {
		if nodes[end].Type == html.TextNode && strings.TrimSpace(nodes[end].Data) == "" {
			end--
		} else {
			break
		}
	}
	if start > end {
		return
	}

	writeIndent(w, depth)
	for i := start; i <= end; i++ {
		isFirst := (i == start)
		isLast := (i == end)
		renderNormalizedInline(w, nodes[i], isFirst, isLast)
	}
	io.WriteString(w, "\n")
}

// writeIndent writes the indentation for the given depth
func writeIndent(w io.Writer, depth int) {
	for range depth {
		io.WriteString(w, "    ")
	}
}

// prettyPrint recursively prints the HTML node with indentation
func prettyPrint(w io.Writer, n *html.Node, depth int) {
	if n.Type == html.DocumentNode {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			prettyPrint(w, c, depth)
		}
		return
	}

	if n.Type == html.CommentNode {
		writeIndent(w, depth)
		io.WriteString(w, "<!--")
		io.WriteString(w, n.Data)
		io.WriteString(w, "-->\n")
		return
	}

	if n.Type == html.DoctypeNode {
		io.WriteString(w, "<!doctype ")
		io.WriteString(w, n.Data)
		io.WriteString(w, ">\n")
		return
	}

	if n.Type == html.TextNode {
		parentTag := ""
		if n.Parent != nil {
			parentTag = n.Parent.Data
		}
		if parentTag == "script" || parentTag == "style" || parentTag == "pre" || parentTag == "textarea" {
			io.WriteString(w, n.Data)
			return
		}
		text := strings.TrimSpace(n.Data)
		if text != "" {
			io.WriteString(w, html.EscapeString(text))
		}
		return
	}

	if n.Data == "script" || n.Data == "style" || n.Data == "pre" || n.Data == "textarea" {
		writeIndent(w, depth)
		io.WriteString(w, "<")
		io.WriteString(w, n.Data)
		for _, attr := range n.Attr {
			io.WriteString(w, " ")
			if attr.Namespace != "" {
				io.WriteString(w, attr.Namespace)
				io.WriteString(w, ":")
			}
			io.WriteString(w, attr.Key)
			io.WriteString(w, `="`)
			io.WriteString(w, html.EscapeString(attr.Val))
			io.WriteString(w, `"`)
		}
		io.WriteString(w, ">")

		var textContent string
		hasNewline := false
		if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
			textContent = n.FirstChild.Data
			hasNewline = strings.Contains(textContent, "\n")
		}

		if hasNewline {
			io.WriteString(w, "\n")
			if n.Data == "script" || n.Data == "style" {
				lines := strings.SplitSeq(textContent, "\n")
				for line := range lines {
					trimmed := strings.TrimSpace(line)
					if trimmed != "" {
						writeIndent(w, depth+1)
						io.WriteString(w, trimmed)
						io.WriteString(w, "\n")
					}
				}
			} else {
				io.WriteString(w, textContent)
			}
			writeIndent(w, depth)
		} else {
			io.WriteString(w, textContent)
		}

		io.WriteString(w, "</")
		io.WriteString(w, n.Data)
		io.WriteString(w, ">\n")
		return
	}

	writeIndent(w, depth)
	io.WriteString(w, "<")
	io.WriteString(w, n.Data)
	for _, attr := range n.Attr {
		io.WriteString(w, " ")
		if attr.Namespace != "" {
			io.WriteString(w, attr.Namespace)
			io.WriteString(w, ":")
		}
		io.WriteString(w, attr.Key)
		io.WriteString(w, `="`)
		io.WriteString(w, html.EscapeString(attr.Val))
		io.WriteString(w, `"`)
	}

	if isSelfClosing(n.Data) {
		io.WriteString(w, " />\n")
		return
	}
	io.WriteString(w, ">")

	if shouldStructure(n) {
		io.WriteString(w, "\n")
		var inlineAccum []*html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if shouldStructure(c) || c.Type == html.CommentNode || isStandaloneTag(c) {
				flushInline(w, inlineAccum, depth+1)
				inlineAccum = nil
				prettyPrint(w, c, depth+1)
			} else {
				inlineAccum = append(inlineAccum, c)
			}
		}
		flushInline(w, inlineAccum, depth+1)
		writeIndent(w, depth)
	} else {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderNormalizedInline(w, c, false, false)
		}
	}

	io.WriteString(w, "</")
	io.WriteString(w, n.Data)
	io.WriteString(w, ">\n")
}

// renderTemplate renders the given template with the given data, writing the result to the response writer
func renderTemplate(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	doc, err := html.Parse(&buf)
	if err != nil {
		log.Printf("html parse error: %v", err)
		w.Write(buf.Bytes())
		return
	}

	var prettyBuf bytes.Buffer
	prettyPrint(&prettyBuf, doc, 0)
	w.Write(prettyBuf.Bytes())
}
