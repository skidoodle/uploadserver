package web

import (
	"embed"
	"html/template"
	"path/filepath"
	"strings"
	"time"
	"uploadserver/internal"
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
	Token   internal.TokenRecord
	Uploads []internal.UploadEntry
	BaseURL string
	CSRF    string
}

// adminPageData is the template data for the admin page.
type adminPageData struct {
	LoggedIn     bool
	IsAdmin      bool
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
}).Parse(uploadsHTML))

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

func init() {
	template.Must(adminTmpl.New("login").Parse(loginHTML))
	template.Must(adminTmpl.New("dashboard").Parse(dashboardHTML))
}
