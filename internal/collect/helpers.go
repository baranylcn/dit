package collect

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

// httpClient is the interface used for HTTP requests (allows testing).
type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

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
	defer func() { _ = f.Close() }()

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
	defer func() { _ = f.Close() }()

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

func fetchHTML(client httpClient, rawURL, userAgent string) (string, int, error) {
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
	defer func() { _ = resp.Body.Close() }()

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

func fetchAndSave(client httpClient, rawURL, pageType, userAgent, outputDir string, index map[string]pageIndexEntry) error {
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

func fetchAndSaveMangled(client httpClient, mangledURL, userAgent, outputDir string, index map[string]pageIndexEntry) (int, error) {
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
		path += segment
		lastSlash = strings.LastIndex(path, "/")
		segment = path[lastSlash+1:]
	}

	pos := rand.IntN(len(segment) + 1)
	ch := byte('a' + rand.IntN(26))
	mangled := segment[:pos] + string(ch) + segment[pos:]

	u.Path = path[:lastSlash+1] + mangled
	return u.String()
}
