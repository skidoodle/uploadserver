package web

import (
	"embed"
	"html/template"
	"time"
	"uploadserver/internal"
)

//go:embed static/admin.gohtml
var adminHTML string

//go:embed static/login.gohtml
var loginHTML string

//go:embed static/dashboard.gohtml
var dashboardHTML string

//go:embed static/login.css static/login.js static/admin.css static/admin.js
var staticFS embed.FS

// adminPageData is the template data for the admin page.
type adminPageData struct {
	LoggedIn bool
	Tokens   []internal.TokenRecord
	Count    int
	Global   internal.Limits // server-wide default quota
	Error    string
	Secret   *newTokenSecret // non-nil when a token was just created
	CSRF     string
}

// newTokenSecret holds the one-time secret displayed after creating a token.
type newTokenSecret struct {
	ID     string
	Role   string
	Secret string
}

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
