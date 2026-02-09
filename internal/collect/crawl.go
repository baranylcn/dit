package collect

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/spf13/cobra"
)

func (c *CLI) newCrawlCommand() *cobra.Command {
	var (
		sitesFile  string
		outputDir  string
		timeout    int
		delay      int
		userAgent  string
		maxTotal   int
		maxPerSite int
		prob404    float64
	)

	cmd := &cobra.Command{
		Use:   "crawl",
		Short: "Crawl websites, follow links, mangle URLs for error/soft_404",
		Example: `  dit-collect crawl --sites sites.txt --output data/pages
  dit-collect crawl --sites sites.txt --output data/pages --max-total 1000 --prob404 0.3`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sites, err := loadLines(sitesFile)
			if err != nil {
				return fmt.Errorf("load sites: %w", err)
			}
			slog.Info("Loaded sites", "count", len(sites))

			index, err := loadIndex(outputDir)
			if err != nil {
				return fmt.Errorf("load index: %w", err)
			}

			client := newHTTPClient(timeout)
			if err := os.MkdirAll(filepath.Join(outputDir, "html"), 0755); err != nil {
				return fmt.Errorf("create html dir: %w", err)
			}

			totalCollected := 0

			for _, site := range sites {
				if maxTotal > 0 && totalCollected >= maxTotal {
					break
				}

				site = strings.TrimSpace(site)
				if site == "" {
					continue
				}
				if !strings.HasPrefix(site, "http") {
					site = "https://" + site
				}

				n, err := crawlSite(client, site, userAgent, outputDir, index, crawlOpts{
					maxPerSite: maxPerSite,
					maxTotal:   maxTotal,
					total:      &totalCollected,
					prob404:    prob404,
					delay:      time.Duration(delay) * time.Millisecond,
				})
				if err != nil {
					slog.Warn("Failed to crawl site", "site", site, "error", err)
					continue
				}

				slog.Info("Finished site", "site", site, "collected", n, "total", totalCollected)

				// Save index periodically
				if totalCollected%50 == 0 {
					if err := saveIndex(outputDir, index); err != nil {
						slog.Warn("Failed to save index", "error", err)
					}
				}
			}

			if err := saveIndex(outputDir, index); err != nil {
				return fmt.Errorf("save index: %w", err)
			}
			slog.Info("Crawl complete", "total", totalCollected, "index_entries", len(index))
			return nil
		},
	}

	cmd.Flags().StringVar(&sitesFile, "sites", "", "File with domain list (one per line)")
	cmd.Flags().StringVar(&outputDir, "output", "data/pages", "Output directory")
	cmd.Flags().IntVar(&timeout, "timeout", 30, "HTTP timeout in seconds")
	cmd.Flags().IntVar(&delay, "delay", 800, "Delay between requests in ms")
	cmd.Flags().StringVar(&userAgent, "user-agent", "Mozilla/5.0 (compatible; dit-collect/1.0)", "User-Agent header")
	cmd.Flags().IntVar(&maxTotal, "max-total", 0, "Max total pages (0=unlimited)")
	cmd.Flags().IntVar(&maxPerSite, "max-per-site", 20, "Max pages per site")
	cmd.Flags().Float64Var(&prob404, "prob404", 0.3, "Probability of mangling a discovered link")
	_ = cmd.MarkFlagRequired("sites")
	return cmd
}

type crawlOpts struct {
	maxPerSite int
	maxTotal   int
	total      *int
	prob404    float64
	delay      time.Duration
}

func crawlSite(client httpClient, siteURL, userAgent, outputDir string, index map[string]pageIndexEntry, opts crawlOpts) (int, error) {
	siteU, err := url.Parse(siteURL)
	if err != nil {
		return 0, err
	}
	siteHost := siteU.Hostname()

	visited := make(map[string]bool)
	collected := 0

	// 1. Fetch homepage as landing page
	html, status, err := fetchHTML(client, siteURL, userAgent)
	if err != nil {
		return 0, fmt.Errorf("homepage: %w", err)
	}
	if status >= 400 || len(html) < 100 {
		return 0, fmt.Errorf("homepage HTTP %d (%d bytes)", status, len(html))
	}

	filename := saveHTMLFile(html, siteURL, outputDir)
	index[filename] = pageIndexEntry{URL: siteURL, PageType: "ln"}
	visited[siteURL] = true
	collected++
	*opts.total++
	slog.Debug("Collected homepage", "url", siteURL, "type", "ln")

	// 2. Extract links from homepage
	links := extractLinks(html, siteU)

	rand.Shuffle(len(links), func(i, j int) { links[i], links[j] = links[j], links[i] })

	// 3. Follow links on same domain
	for _, link := range links {
		if collected >= opts.maxPerSite {
			break
		}
		if opts.maxTotal > 0 && *opts.total >= opts.maxTotal {
			break
		}

		linkU, err := url.Parse(link)
		if err != nil {
			continue
		}

		if linkU.Hostname() != siteHost {
			continue
		}

		normalized := normalizeURL(link)
		if visited[normalized] {
			continue
		}
		visited[normalized] = true

		if skipURL(linkU) {
			continue
		}

		time.Sleep(opts.delay)

		pageType := detectPageType(linkU)

		linkHTML, linkStatus, err := fetchHTML(client, link, userAgent)
		if err != nil {
			slog.Debug("Failed to fetch link", "url", link, "error", err)
			continue
		}

		if linkStatus == 200 && len(linkHTML) >= 100 && pageType != "" {
			fn := saveHTMLFile(linkHTML, link, outputDir)
			index[fn] = pageIndexEntry{URL: link, PageType: pageType}
			collected++
			*opts.total++
			slog.Debug("Collected link", "url", link, "type", pageType)

			subLinks := extractLinks(linkHTML, siteU)
			links = append(links, subLinks...)
		}

		// Mangle with probability prob404
		if rand.Float64() < opts.prob404 && len(linkU.Path) > 1 {
			if opts.maxTotal > 0 && *opts.total >= opts.maxTotal {
				break
			}
			if collected >= opts.maxPerSite {
				break
			}

			time.Sleep(opts.delay)
			mangledURL := manglePath(link)
			if mangledURL != "" && !visited[mangledURL] {
				visited[mangledURL] = true
				mangledHTML, mangledStatus, err := fetchHTML(client, mangledURL, userAgent)
				if err != nil {
					slog.Debug("Failed mangled", "url", mangledURL, "error", err)
					continue
				}

				if len(mangledHTML) >= 100 && (mangledStatus == 200 || mangledStatus == 404) {
					mangledType := "s4"
					if mangledStatus == 404 {
						mangledType = "er"
					}
					fn := saveHTMLFile(mangledHTML, mangledURL, outputDir)
					index[fn] = pageIndexEntry{URL: mangledURL, PageType: mangledType}
					collected++
					*opts.total++
					slog.Debug("Collected mangled", "url", mangledURL, "status", mangledStatus, "type", mangledType)
				}
			}
		}
	}

	return collected, nil
}

func extractLinks(htmlStr string, base *url.URL) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return nil
	}

	var links []string
	seen := make(map[string]bool)
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists || href == "" {
			return
		}

		if strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") {
			return
		}

		u, err := url.Parse(href)
		if err != nil {
			return
		}
		resolved := base.ResolveReference(u).String()

		if !seen[resolved] {
			seen[resolved] = true
			links = append(links, resolved)
		}
	})

	return links
}

func detectPageType(u *url.URL) string {
	path := strings.ToLower(u.Path)
	host := strings.ToLower(u.Hostname())

	if matchAny(path, "/login", "/signin", "/sign-in", "/sign_in", "/wp-login", "/sso/start", "/auth/login", "/user/login", "/account/login", "/accounts/login") {
		return "lg"
	}

	if matchAny(path, "/register", "/signup", "/sign-up", "/sign_up", "/join", "/create-account", "/user/register", "/accounts/emailsignup") {
		return "rg"
	}

	if matchAny(path, "/forgot", "/reset-password", "/password/reset", "/password/new", "/account-recovery", "/account/recover", "/password_reset", "/forgot_password", "/forgot-password") {
		return "pr"
	}

	if matchAny(path, "/contact", "/contact-us", "/contact_us") {
		return "ct"
	}

	if matchAny(path, "/search") || u.Query().Get("q") != "" || u.Query().Get("s") != "" || u.Query().Get("query") != "" {
		return "sr"
	}

	if matchAny(path, "/blog", "/post/", "/posts/", "/article/", "/articles/", "/news/") ||
		strings.HasPrefix(host, "blog.") || strings.HasPrefix(host, "engineering.") {
		return "bl"
	}

	if matchAny(path, "/product/", "/products/", "/dp/", "/item/", "/itm/", "/p/", "/listing/") {
		return "pd"
	}

	return ""
}

func matchAny(path string, patterns ...string) bool {
	for _, p := range patterns {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}

func skipURL(u *url.URL) bool {
	path := strings.ToLower(u.Path)
	for _, ext := range []string{".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".pdf", ".zip", ".xml", ".json", ".woff", ".woff2", ".ttf", ".mp4", ".mp3", ".webp", ".avif"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func normalizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Fragment = ""
	s := u.String()
	return strings.TrimRight(s, "/")
}
