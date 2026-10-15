// Package clientip resolves the client IP source used by rate limits and audit records.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	SettingKey       = "client_ip_source"
	RemoteAddrSource = "REMOTE_ADDR"
	contextKey       = "picsky_client_ip"
)

// Source is an IP source observed on the current request.
type Source struct {
	Source string `json:"source"`
	IP     string `json:"ip"`
}

// NormalizeSource canonicalizes a configured source name.
func NormalizeSource(source string) string {
	source = strings.ToUpper(strings.TrimSpace(source))
	if source == "" {
		return RemoteAddrSource
	}
	return source
}

// ValidSource accepts the socket address or a CGI-style request-header name.
func ValidSource(source string) bool {
	source = NormalizeSource(source)
	if source == RemoteAddrSource {
		return true
	}
	if !strings.HasPrefix(source, "HTTP_") || len(source) <= len("HTTP_") || len(source) > 256 {
		return false
	}
	for i := len("HTTP_"); i < len(source); i++ {
		char := source[i]
		if !((char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return true
}

// Resolve returns the first valid IP from the configured source. Missing or
// malformed header values fall back to the socket peer, never to another header.
func Resolve(request *http.Request, source string) string {
	if request == nil {
		return ""
	}
	remoteIP := parseIP(request.RemoteAddr)
	source = NormalizeSource(source)
	if source == RemoteAddrSource || !ValidSource(source) {
		return remoteIP
	}

	for name, values := range request.Header {
		if !headerSourceMatches(name, source) {
			continue
		}
		for _, value := range values {
			if ip := firstIP(value); ip != "" {
				return ip
			}
		}
	}
	return remoteIP
}

// Candidates lists the socket address and request headers containing valid IPs.
// The socket address is first; header sources are sorted for stable UI ordering.
func Candidates(request *http.Request) []Source {
	if request == nil {
		return []Source{}
	}
	candidates := make([]Source, 0, len(request.Header)+1)

	if ip := parseIP(request.RemoteAddr); ip != "" {
		candidates = append(candidates, Source{Source: RemoteAddrSource, IP: ip})
	}

	names := make([]string, 0, len(request.Header))
	for name := range request.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		source := headerSource(name)
		if !ValidSource(source) {
			continue
		}
		if _, exists := seen[source]; exists {
			continue
		}
		for _, value := range request.Header.Values(name) {
			if ip := firstIP(value); ip != "" {
				candidates = append(candidates, Source{Source: source, IP: ip})
				seen[source] = struct{}{}
				break
			}
		}
	}
	return candidates
}

// SetContext stores the resolved address for all handlers on this request.
func SetContext(c *gin.Context, source string) {
	c.Set(contextKey, Resolve(c.Request, source))
}

// FromContext returns the resolved request address, falling back to the socket peer.
func FromContext(c *gin.Context) string {
	if value, exists := c.Get(contextKey); exists {
		if ip, ok := value.(string); ok {
			return ip
		}
	}
	return Resolve(c.Request, RemoteAddrSource)
}

func headerSource(name string) string {
	return "HTTP_" + strings.ReplaceAll(strings.ToUpper(name), "-", "_")
}

func headerSourceMatches(name, source string) bool {
	if len(source) != len("HTTP_")+len(name) || !strings.HasPrefix(source, "HTTP_") {
		return false
	}
	for i := range name {
		char := name[i]
		if char == '-' {
			char = '_'
		} else if char >= 'a' && char <= 'z' {
			char -= 'a' - 'A'
		}
		if char != source[len("HTTP_")+i] {
			return false
		}
	}
	return true
}

func firstIP(value string) string {
	for len(value) > 0 {
		end := strings.IndexAny(value, ",; \t\r\n\"'")
		part := value
		if end >= 0 {
			part, value = value[:end], value[end+1:]
		} else {
			value = ""
		}
		candidate := strings.TrimSpace(part)
		if len(candidate) >= 4 && strings.EqualFold(candidate[:4], "for=") {
			candidate = candidate[4:]
		}
		if ip := parseIP(candidate); ip != "" {
			return ip
		}
	}
	return ""
}

func parseIP(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return ""
	}
	return ip.Unmap().String()
}
