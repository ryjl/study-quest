package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"
	"studyquest/backend/internal/service"
)

// Code split from admin_reading.go for navigability.
// Reading import preview/execute + whitelist suggest + URL/HTML domain helpers.

func (h *adminHandler) PreviewReadingImport(c *gin.Context) {
	path := strings.TrimSpace(c.Query("path"))
	if path == "" {
		path = "/"
	}
	tree, err := h.readingImportService.PreviewReadingFolder(path, parseSourceIDQuery(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to scan folder: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, tree)
}

// ExecuteReadingImport creates a ReadingSeries + ReadingBook rows from a preview tree.
// POST /admin/api/reading-import/execute
func (h *adminHandler) ExecuteReadingImport(c *gin.Context) {
	var req service.ExecuteReadingImportRequest
	if !bindJSON(c, &req) { return }
	if err := h.readingImportService.ExecuteReadingImport(&req); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// ── Whitelist suggestion ──

// SuggestWhitelist fetches the article URL server-side, extracts all unique
// domains from the static HTML (img/script/link/iframe sources + CSS url()),
// and returns them as a suggested whitelist for the admin to review.
//
// Known limitation: JS-injected resources (e.g. mpvideo.qpic.cn video shards)
// won't appear in static HTML. The 4 core WeChat domains cover the vast
// majority of articles; the admin can manually add any missing domain.
func (h *adminHandler) SuggestWhitelist(c *gin.Context) {
	var req struct {
		SourceURL string `json:"source_url" binding:"required"`
	}
	if !bindJSON(c, &req) { return }

	domains, err := extractDomainsFromURL(req.SourceURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch article: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"domains": domains})
}

// urlAttrSet is the set of HTML attributes whose values may contain resource
// URLs. The critical entry here is "data-src" — WeChat 公众号 articles store
// the real image URL in data-src (for lazy loading), NOT in src. An extractor
// that only checks src will miss every body image.
var urlAttrSet = map[string]bool{
	"src":          true,
	"href":         true,
	"data-src":     true,
	"data-srcset":  true,
	"srcset":       true,
	"poster":       true,
	"data-url":     true,
	"data-original": true,
}

// cssURLRe matches url(...) references inside inline CSS / style attributes.
var cssURLRe = regexp.MustCompile(`url\(\s*['"]?([^'")\s]+)`)

// extractDomainsFromURL fetches the given URL and extracts all unique hostnames
// from resource references in the static HTML. Returns a sorted slice. Always
// includes the source URL's own host as a fallback — JS-rendered pages (e.g.
// Vue/Nuxt H5 apps) may have no resource URLs in their static HTML, but the
// page itself still loads from its own host and internal navigation within that
// host must be allowed.
func extractDomainsFromURL(rawURL string) ([]string, error) {
	client := &http.Client{Timeout: 12 * 1000_000_000} // 12s
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	// Mobile + MicroMessenger UA: WeChat serves mobile-optimized HTML to this UA,
	// which is what the in-app WebView actually renders.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/120.0.0.0 Mobile Safari/537.36 MMWEBID/1234 MicroMessenger/8.0.40")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("article URL returned HTTP " + resp.Status)
	}

	hosts := collectDomainsFromHTML(resp.Body)

	// Always include the source URL's own host. For JS-rendered H5 pages where
	// the static HTML has no resource URLs, this is the only domain we can find
	// — but it's also the most important one (the page itself + internal nav).
	if sourceURL, err := url.Parse(rawURL); err == nil && sourceURL.Host != "" {
		hosts[sourceURL.Host] = true
	}

	out := make([]string, 0, len(hosts))
	for h := range hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// collectDomainsFromHTML streams the HTML body through x/net/html tokenizer,
// extracting hostnames from all resource-bearing attributes + inline CSS url().
func collectDomainsFromHTML(body io.Reader) map[string]bool {
	hosts := make(map[string]bool)
	collect := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "data:") || strings.HasPrefix(raw, "blob:") {
			return
		}
		// srcset may contain multiple "url descriptor, url descriptor" entries.
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			// Strip descriptor (e.g. "url 2x" → "url").
			if idx := strings.IndexByte(part, ' '); idx > 0 {
				part = part[:idx]
			}
			if u, err := url.Parse(part); err == nil && u.Host != "" {
				hosts[u.Host] = true
			}
		}
	}

	z := html.NewTokenizer(body)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			for {
				k, v, more := z.TagAttr()
				if urlAttrSet[string(k)] {
					collect(string(v))
				}
				// Also scan style="..." for url(...) references.
				if string(k) == "style" {
					for _, m := range cssURLRe.FindAllStringSubmatch(string(v), -1) {
						collect(m[1])
					}
				}
				if !more {
					break
				}
			}
		}
	}

	return hosts
}

// normalizeWhitelistJSON coerces the input into a valid JSON []string. Accepts
// an already-JSON array, a comma-separated string, or empty (→ "[]"). This lets
// the admin SPA send either a raw JSON array string or a simple comma list.
func normalizeWhitelistJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "[]"
	}
	// If it already parses as a []string, keep it as-is.
	var arr []string
	if json.Unmarshal([]byte(raw), &arr) == nil {
		if arr == nil {
			return "[]"
		}
		return raw
	}
	// Treat as comma-separated.
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	buf, _ := json.Marshal(out)
	return string(buf)
}
