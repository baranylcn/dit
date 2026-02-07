package main

import (
	"bufio"
	"crypto/md5"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/spf13/cobra"
)

// seedEntry represents a single entry in the seed file (JSONL).
type seedEntry struct {
	URL          string `json:"url"`
	ExpectedType string `json:"expected_type"`
	Mangle       bool   `json:"mangle,omitempty"`
}

// pageIndexEntry matches the data/pages/index.json format.
type pageIndexEntry struct {
	URL      string `json:"url"`
	PageType string `json:"page_type"`
}

var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:     "dit-collect",
		Short:   "Collect HTML pages for page type classifier training",
		Version: version,
	}

	rootCmd.AddCommand(collectCmd(), crawlCmd(), genSeedCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func collectCmd() *cobra.Command {
	var (
		outputDir  string
		seedFile   string
		timeout    int
		delay      int
		userAgent  string
		verbose    bool
		maxPages   int
		mangleOnly bool
	)

	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Fetch pages from seed URLs and save to data/pages/",
		Example: `  dit-collect collect --seed seeds.jsonl --output data/pages
  dit-collect collect --seed seeds.jsonl --output data/pages --mangle-only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if verbose {
				slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
			}

			seeds, err := loadSeeds(seedFile)
			if err != nil {
				return fmt.Errorf("load seeds: %w", err)
			}
			slog.Info("Loaded seeds", "count", len(seeds))

			index, err := loadIndex(outputDir)
			if err != nil {
				return fmt.Errorf("load index: %w", err)
			}

			client := newHTTPClient(timeout)
			if err := os.MkdirAll(filepath.Join(outputDir, "html"), 0755); err != nil {
				return fmt.Errorf("create html dir: %w", err)
			}

			collected := 0
			for _, seed := range seeds {
				if maxPages > 0 && collected >= maxPages {
					break
				}

				if !mangleOnly {
					if err := fetchAndSave(client, seed.URL, seed.ExpectedType, userAgent, outputDir, index); err != nil {
						slog.Warn("Failed to fetch", "url", seed.URL, "error", err)
					} else {
						collected++
						slog.Info("Collected", "url", seed.URL, "type", seed.ExpectedType, "total", collected)
					}
				}

				if seed.Mangle {
					mangledURL := manglePath(seed.URL)
					if mangledURL != "" {
						if maxPages > 0 && collected >= maxPages {
							break
						}
						time.Sleep(time.Duration(delay) * time.Millisecond)

						status, err := fetchAndSaveMangled(client, mangledURL, userAgent, outputDir, index)
						if err != nil {
							slog.Warn("Failed to fetch mangled", "url", mangledURL, "error", err)
						} else {
							collected++
							pageType := "s4"
							if status == 404 {
								pageType = "er"
							}
							slog.Info("Collected mangled", "url", mangledURL, "status", status, "type", pageType, "total", collected)
						}
					}
				}

				if delay > 0 {
					time.Sleep(time.Duration(delay) * time.Millisecond)
				}
			}

			if err := saveIndex(outputDir, index); err != nil {
				return fmt.Errorf("save index: %w", err)
			}
			slog.Info("Collection complete", "total", collected, "index_entries", len(index))
			return nil
		},
	}

	cmd.Flags().StringVar(&seedFile, "seed", "", "Path to seed file (JSONL)")
	cmd.Flags().StringVar(&outputDir, "output", "data/pages", "Output directory")
	cmd.Flags().IntVar(&timeout, "timeout", 30, "HTTP timeout in seconds")
	cmd.Flags().IntVar(&delay, "delay", 1000, "Delay between requests in ms")
	cmd.Flags().StringVar(&userAgent, "user-agent", "Mozilla/5.0 (compatible; dit-collect/1.0)", "User-Agent header")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	cmd.Flags().IntVar(&maxPages, "max", 0, "Max pages to collect (0=unlimited)")
	cmd.Flags().BoolVar(&mangleOnly, "mangle-only", false, "Only collect mangled URLs")
	_ = cmd.MarkFlagRequired("seed")
	return cmd
}

// crawlCmd crawls real websites following links, like soft-404's spider.
// For each site: fetch homepage (landing), follow discovered links,
// auto-label by URL pattern, mangle random links for error/soft_404.
func crawlCmd() *cobra.Command {
	var (
		sitesFile  string
		outputDir  string
		timeout    int
		delay      int
		userAgent  string
		verbose    bool
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
			if verbose {
				slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
			}

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
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
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

func crawlSite(client *http.Client, siteURL, userAgent, outputDir string, index map[string]pageIndexEntry, opts crawlOpts) (int, error) {
	siteU, err := url.Parse(siteURL)
	if err != nil {
		return 0, err
	}
	siteHost := siteU.Hostname()

	// Track visited URLs to avoid duplicates
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

	// Shuffle links for variety
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

		// Same host only
		if linkU.Hostname() != siteHost {
			continue
		}

		// Skip already visited
		normalized := normalizeURL(link)
		if visited[normalized] {
			continue
		}
		visited[normalized] = true

		// Skip non-page links
		if skipURL(linkU) {
			continue
		}

		time.Sleep(opts.delay)

		// Auto-detect page type from URL
		pageType := detectPageType(linkU)

		// Fetch the page
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

			// Also extract links from this page for deeper crawling
			subLinks := extractLinks(linkHTML, siteU)
			links = append(links, subLinks...)
		}

		// Mangle with probability prob404 (only for links with path > 1 char)
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

// extractLinks extracts all <a href> links from HTML, resolving relative URLs.
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

		// Skip fragments, javascript, mailto
		if strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") {
			return
		}

		// Resolve relative URLs
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

// detectPageType auto-labels a URL based on path patterns.
// Returns "" if no pattern matches (page won't be saved).
func detectPageType(u *url.URL) string {
	path := strings.ToLower(u.Path)
	host := strings.ToLower(u.Hostname())

	// Login
	if matchAny(path, "/login", "/signin", "/sign-in", "/sign_in", "/wp-login", "/sso/start", "/auth/login", "/user/login", "/account/login", "/accounts/login") {
		return "lg"
	}

	// Registration
	if matchAny(path, "/register", "/signup", "/sign-up", "/sign_up", "/join", "/create-account", "/user/register", "/accounts/emailsignup") {
		return "rg"
	}

	// Password reset
	if matchAny(path, "/forgot", "/reset-password", "/password/reset", "/password/new", "/account-recovery", "/account/recover", "/password_reset", "/forgot_password", "/forgot-password") {
		return "pr"
	}

	// Contact
	if matchAny(path, "/contact", "/contact-us", "/contact_us") {
		return "ct"
	}

	// Search
	if matchAny(path, "/search") || u.Query().Get("q") != "" || u.Query().Get("s") != "" || u.Query().Get("query") != "" {
		return "sr"
	}

	// Blog
	if matchAny(path, "/blog", "/post/", "/posts/", "/article/", "/articles/", "/news/") ||
		strings.HasPrefix(host, "blog.") || strings.HasPrefix(host, "engineering.") {
		return "bl"
	}

	// Product
	if matchAny(path, "/product/", "/products/", "/dp/", "/item/", "/itm/", "/p/", "/listing/") {
		return "pd"
	}

	return ""
}

// matchAny checks if path contains any of the given patterns.
func matchAny(path string, patterns ...string) bool {
	for _, p := range patterns {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}

// skipURL filters out non-page URLs (images, scripts, etc.)
func skipURL(u *url.URL) bool {
	path := strings.ToLower(u.Path)
	for _, ext := range []string{".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".pdf", ".zip", ".xml", ".json", ".woff", ".woff2", ".ttf", ".mp4", ".mp3", ".webp", ".avif"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// normalizeURL strips fragment and trailing slash for dedup.
func normalizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Fragment = ""
	s := u.String()
	return strings.TrimRight(s, "/")
}

func genSeedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-seeds",
		Short: "Generate seed file from common URL patterns",
		Example: `  dit-collect gen-seeds --domains domains.txt --output seeds.jsonl
  dit-collect gen-seeds --domains domains.txt --output seeds.jsonl --types login,registration`,
		RunE: func(cmd *cobra.Command, args []string) error {
			domainsFile, _ := cmd.Flags().GetString("domains")
			output, _ := cmd.Flags().GetString("output")
			types, _ := cmd.Flags().GetString("types")

			domains, err := loadLines(domainsFile)
			if err != nil {
				return fmt.Errorf("load domains: %w", err)
			}

			typeList := strings.Split(types, ",")
			typePatterns := getTypePatterns()

			f, err := os.Create(output)
			if err != nil {
				return err
			}
			defer f.Close()

			enc := json.NewEncoder(f)
			count := 0
			for _, domain := range domains {
				domain = strings.TrimSpace(domain)
				if domain == "" {
					continue
				}
				if !strings.HasPrefix(domain, "http") {
					domain = "https://" + domain
				}

				for _, tp := range typeList {
					tp = strings.TrimSpace(tp)
					paths, ok := typePatterns[tp]
					if !ok {
						continue
					}
					for _, path := range paths {
						seed := seedEntry{
							URL:          domain + path,
							ExpectedType: tp,
							Mangle:       tp == "error" || tp == "soft_404",
						}
						if err := enc.Encode(seed); err != nil {
							return err
						}
						count++
					}
				}

				if containsType(typeList, "landing") {
					seed := seedEntry{URL: domain, ExpectedType: "landing", Mangle: true}
					if err := enc.Encode(seed); err != nil {
						return err
					}
					count++
				}
			}

			fmt.Printf("Generated %d seed entries to %s\n", count, output)
			return nil
		},
	}
	cmd.Flags().String("domains", "", "File with domain list (one per line)")
	cmd.Flags().String("output", "seeds.jsonl", "Output seed file")
	cmd.Flags().String("types", "login,registration,search,contact,password_reset,error,soft_404,admin,landing", "Page types to generate seeds for")
	_ = cmd.MarkFlagRequired("domains")
	return cmd
}

// --- shared helpers ---

func newHTTPClient(timeoutSec int) *http.Client {
	return &http.Client{
		Timeout: time.Duration(timeoutSec) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

func loadSeeds(path string) ([]seedEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var seeds []seedEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var s seedEntry
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			slog.Warn("Skipping invalid seed line", "line", line, "error", err)
			continue
		}
		seeds = append(seeds, s)
	}
	return seeds, scanner.Err()
}

func loadLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

func loadIndex(dir string) (map[string]pageIndexEntry, error) {
	path := filepath.Join(dir, "index.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]pageIndexEntry), nil
		}
		return nil, err
	}
	var index map[string]pageIndexEntry
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return index, nil
}

func saveIndex(dir string, index map[string]pageIndexEntry) error {
	data, err := json.MarshalIndent(index, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), data, 0644)
}

func fetchHTML(client *http.Client, rawURL, userAgent string) (string, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body := make([]byte, 0, 1024*1024)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			if len(body) > 5*1024*1024 {
				break
			}
		}
		if err != nil {
			break
		}
	}

	return string(body), resp.StatusCode, nil
}

func fetchAndSave(client *http.Client, rawURL, pageType, userAgent, outputDir string, index map[string]pageIndexEntry) error {
	html, status, err := fetchHTML(client, rawURL, userAgent)
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("HTTP %d", status)
	}
	if len(html) < 100 {
		return fmt.Errorf("response too short (%d bytes)", len(html))
	}

	filename := saveHTMLFile(html, rawURL, outputDir)
	index[filename] = pageIndexEntry{URL: rawURL, PageType: pageType}
	return nil
}

func fetchAndSaveMangled(client *http.Client, mangledURL, userAgent, outputDir string, index map[string]pageIndexEntry) (int, error) {
	html, status, err := fetchHTML(client, mangledURL, userAgent)
	if err != nil {
		return 0, err
	}
	if len(html) < 100 {
		return status, fmt.Errorf("response too short (%d bytes)", len(html))
	}

	if status != 200 && status != 404 {
		return status, fmt.Errorf("unexpected status %d for mangled URL", status)
	}

	pageType := "s4"
	if status == 404 {
		pageType = "er"
	}

	filename := saveHTMLFile(html, mangledURL, outputDir)
	index[filename] = pageIndexEntry{URL: mangledURL, PageType: pageType}
	return status, nil
}

func saveHTMLFile(html, rawURL, outputDir string) string {
	hash := fmt.Sprintf("%x", md5.Sum([]byte(rawURL)))
	filename := "html/" + hash[:12] + ".html"
	path := filepath.Join(outputDir, filename)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, []byte(html), 0644)
	return filename
}

func manglePath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	path := u.Path
	if path == "" || path == "/" {
		path = "/index"
	}

	lastSlash := strings.LastIndex(path, "/")
	if lastSlash < 0 {
		lastSlash = 0
	}
	segment := path[lastSlash+1:]
	if segment == "" {
		segment = "page"
		path = path + segment
		lastSlash = strings.LastIndex(path, "/")
		segment = path[lastSlash+1:]
	}

	pos := rand.IntN(len(segment) + 1)
	ch := byte('a' + rand.IntN(26))
	mangled := segment[:pos] + string(ch) + segment[pos:]

	u.Path = path[:lastSlash+1] + mangled
	return u.String()
}

func getTypePatterns() map[string][]string {
	return map[string][]string{
		"login":          {"/login", "/signin", "/account/login", "/wp-login.php", "/user/login", "/auth/login"},
		"registration":   {"/register", "/signup", "/join", "/create-account", "/user/register"},
		"search":         {"/search", "/search?q=test", "/?s=test"},
		"contact":        {"/contact", "/contact-us", "/about/contact"},
		"password_reset": {"/forgot-password", "/reset-password", "/account/recover", "/password/reset"},
		"admin":          {"/admin", "/wp-admin", "/dashboard", "/admin/login"},
		"error":          {"/this-page-does-not-exist-404-test", "/nonexistent-page-xyz"},
		"soft_404":       {"/this-page-does-not-exist-404-test"},
	}
}

func containsType(types []string, tp string) bool {
	for _, t := range types {
		if strings.TrimSpace(t) == tp {
			return true
		}
	}
	return false
}
