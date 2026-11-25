// Package sites manages websites: validation, Nginx vhost generation, applying
// configs safely (nginx -t with rollback) and Let's Encrypt certificates.
//
// Everything that ends up inside an Nginx config is validated against a strict
// allowlist first, so a domain or proxy URL can never inject directives.
package sites

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

var (
	domainRe   = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{1,62}$`)
	pathCharRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]*$`)
	sockRe     = regexp.MustCompile(`^/run/php/[A-Za-z0-9._-]+\.sock$`)
)

// NormalizeDomain lowercases and validates a hostname. Wildcards, ports,
// underscores and IP addresses are rejected.
func NormalizeDomain(s string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	if d == "" {
		return "", errors.New("domain is required")
	}
	if len(d) > 253 || !domainRe.MatchString(d) {
		return "", fmt.Errorf("%q is not a valid domain name (example: example.com)", s)
	}
	return d, nil
}

// NormalizeAliases validates a list of extra hostnames, dropping blanks and duplicates.
func NormalizeAliases(primary string, in []string) ([]string, error) {
	seen := map[string]bool{primary: true}
	out := []string{}
	for _, a := range in {
		if strings.TrimSpace(a) == "" {
			continue
		}
		d, err := NormalizeDomain(a)
		if err != nil {
			return nil, err
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) > 20 {
		return nil, errors.New("at most 20 aliases per site")
	}
	return out, nil
}

// NormalizeProxyURL accepts only scheme://host[:port] with an http or https
// scheme: no credentials, path, query or fragment. That is all Nginx needs for
// a reverse proxy and it leaves nothing to smuggle into the config.
func NormalizeProxyURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("proxy URL is required, e.g. http://127.0.0.1:3000")
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("proxy URL must look like http://127.0.0.1:3000")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("proxy URL must be only scheme://host:port (no path, query or credentials)")
	}
	host := u.Hostname()
	port := u.Port()
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", errors.New("proxy URL has an invalid port")
		}
	}
	switch {
	case net.ParseIP(host) != nil:
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	case host == "localhost" || domainRe.MatchString(strings.ToLower(host)) || singleLabelRe.MatchString(host):
		host = strings.ToLower(host)
	default:
		return "", errors.New("proxy URL has an invalid host")
	}
	out := u.Scheme + "://" + host
	if port != "" {
		out += ":" + port
	}
	return out, nil
}

var singleLabelRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`) // docker service names etc.

// NormalizeRoot checks that a document root lies inside webRoot.
func NormalizeRoot(webRoot, root string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if !pathCharRe.MatchString(root) {
		return "", errors.New("document root may only contain letters, digits and . _ - /")
	}
	wr := filepath.Clean(webRoot)
	if root != wr && !strings.HasPrefix(root, wr+"/") {
		return "", fmt.Errorf("document root must be inside %s", wr)
	}
	if root == wr {
		return "", fmt.Errorf("document root must be a folder inside %s, not %s itself", wr, wr)
	}
	return root, nil
}

// ValidPHPSocket reports whether s looks like a PHP-FPM socket path.
func ValidPHPSocket(s string) bool { return sockRe.MatchString(s) && !strings.Contains(s, "..") }

// ValidType reports whether t is a known site type.
func ValidType(t string) bool {
	return t == store.SiteStatic || t == store.SiteProxy || t == store.SitePHP
}
