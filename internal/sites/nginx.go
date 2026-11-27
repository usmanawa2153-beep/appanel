package sites

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

// RenderOptions are server-wide paths used while rendering a vhost.
type RenderOptions struct {
	ACMERoot       string // webroot used for HTTP-01 challenges
	LetsEncryptDir string // e.g. /etc/letsencrypt
	LogDir         string // e.g. /var/log/nginx
}

type vhostData struct {
	Site      store.Site
	Names     string
	Opt       RenderOptions
	CertPath  string
	KeyPath   string
	Mode      string // "http" | "both" | "redirect"
	Content   string
	ACMEBlock string
}

// Mode values: how port 80 / 443 are served.
const (
	modeHTTP     = "http"     // no certificate: everything on 80
	modeBoth     = "both"     // certificate, HTTP still serves the site
	modeRedirect = "redirect" // certificate, HTTP redirects to HTTPS
)

var vhostTmpl = template.Must(template.New("vhost").Parse(`# Managed by Appanel. Manual edits to this file will be overwritten.
{{- if eq .Mode "redirect"}}
server {
    listen 80;
    listen [::]:80;
    server_name {{.Names}};
{{.ACMEBlock}}
    location / {
        return 301 https://$host$request_uri;
    }
}
{{- else}}
server {
    listen 80;
    listen [::]:80;
    server_name {{.Names}};
    access_log {{.Opt.LogDir}}/{{.Site.Domain}}.access.log;
    error_log {{.Opt.LogDir}}/{{.Site.Domain}}.error.log;
{{.ACMEBlock}}
{{.Content}}
}
{{- end}}
{{- if ne .Mode "http"}}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name {{.Names}};
    access_log {{.Opt.LogDir}}/{{.Site.Domain}}.access.log;
    error_log {{.Opt.LogDir}}/{{.Site.Domain}}.error.log;

    ssl_certificate {{.CertPath}};
    ssl_certificate_key {{.KeyPath}};
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;
{{.Content}}
}
{{- end}}
`))

// RenderVhost returns the Nginx config for a site. The site must already have
// been validated; Render re-checks the pieces it interpolates as defence in depth.
func RenderVhost(s store.Site, o RenderOptions) (string, error) {
	if _, err := NormalizeDomain(s.Domain); err != nil {
		return "", err
	}
	for _, a := range s.Aliases {
		if _, err := NormalizeDomain(a); err != nil {
			return "", err
		}
	}
	for _, p := range []string{o.ACMERoot, o.LetsEncryptDir, o.LogDir} {
		if !pathCharRe.MatchString(p) {
			return "", fmt.Errorf("unsafe path in render options: %q", p)
		}
	}
	content, err := contentBlock(s)
	if err != nil {
		return "", err
	}
	mode := modeHTTP
	if s.SSL {
		mode = modeBoth
		if s.ForceHTTPS {
			mode = modeRedirect
		}
	}
	certDir := s.Domain
	if s.CertName != "" {
		certDir = s.CertName
	}
	d := vhostData{
		Site:     s,
		Names:    strings.Join(s.Names(), " "),
		Opt:      o,
		CertPath: o.LetsEncryptDir + "/live/" + certDir + "/fullchain.pem",
		KeyPath:  o.LetsEncryptDir + "/live/" + certDir + "/privkey.pem",
		Mode:     mode,
		Content:  content,
		ACMEBlock: "    location ^~ /.well-known/acme-challenge/ {\n" +
			"        root " + o.ACMERoot + ";\n" +
			"        default_type text/plain;\n" +
			"    }",
	}
	var b bytes.Buffer
	if err := vhostTmpl.Execute(&b, d); err != nil {
		return "", err
	}
	return b.String(), nil
}

func contentBlock(s store.Site) (string, error) {
	switch s.Type {
	case store.SiteStatic:
		if !pathCharRe.MatchString(s.Root) {
			return "", errors.New("invalid document root")
		}
		return "    root " + s.Root + ";\n" +
			"    index index.html index.htm;\n\n" +
			"    location / {\n        try_files $uri $uri/ =404;\n    }\n\n" +
			"    location ~ /\\.(?!well-known) {\n        deny all;\n    }", nil
	case store.SitePHP:
		if !pathCharRe.MatchString(s.Root) || !ValidPHPSocket(s.PHPSocket) {
			return "", errors.New("invalid document root or PHP socket")
		}
		return "    root " + s.Root + ";\n" +
			"    index index.php index.html index.htm;\n\n" +
			"    location / {\n        try_files $uri $uri/ /index.php?$query_string;\n    }\n\n" +
			"    location ~ \\.php$ {\n" +
			"        include snippets/fastcgi-php.conf;\n" +
			"        fastcgi_pass unix:" + s.PHPSocket + ";\n    }\n\n" +
			"    location ~ /\\.(?!well-known) {\n        deny all;\n    }", nil
	case store.SiteProxy:
		u, err := NormalizeProxyURL(s.ProxyURL)
		if err != nil {
			return "", err
		}
		extra := ""
		if s.Panel {
			// Uploads of any size and a terminal that may sit idle for hours.
			extra = "        client_max_body_size 0;\n        proxy_request_buffering off;\n        proxy_read_timeout 1d;\n        proxy_send_timeout 1d;\n"
		}
		return "    location / {\n" + extra +
			"        proxy_pass " + u + ";\n" +
			"        proxy_http_version 1.1;\n" +
			"        proxy_set_header Host $host;\n" +
			"        proxy_set_header X-Real-IP $remote_addr;\n" +
			"        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n" +
			"        proxy_set_header X-Forwarded-Proto $scheme;\n" +
			"        proxy_set_header Upgrade $http_upgrade;\n" +
			"        proxy_set_header Connection $connection_upgrade;\n" +
			"    }", nil
	}
	return "", fmt.Errorf("unknown site type %q", s.Type)
}

// UpgradeMap is installed once in conf.d; the proxy vhosts refer to $connection_upgrade.
const UpgradeMap = `# Managed by Appanel.
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
`
