package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/happyhackingspace/dit"
	"github.com/spf13/cobra"
)

const modelURL = "https://huggingface.co/datasets/happyhackingspace/dit/resolve/main/model.json"

func (c *CLI) newRunCommand() *cobra.Command {
	var modelPath string
	var threshold float64
	var proba bool

	cmd := &cobra.Command{
		Use:   "run [url-or-file]",
		Short: "Classify page type and forms in a URL, HTML file, or stdin",
		Args:  cobra.MaximumNArgs(1),
		Example: `  # Classify a URL directly
  dit run https://github.com/login

  # Classify a local HTML file
  dit run login.html

  # Pipe HTML content from a file
  cat login.html | dit run

  # Pipe a URL from stdin
  echo "https://github.com/login" | dit run

  # Pipe HTML content from a URL using curl
  curl -s https://github.com/login | dit run

  # Show probability scores
  dit run https://github.com/login --proba

  # Use custom probability threshold
  dit run https://github.com/login --proba --threshold 0.1

  # Use custom model file
  dit run login.html --model custom.json

  # Silent mode (no banner)
  dit run https://github.com/login -s

  # Verbose mode with debug output
  dit run https://github.com/login -v`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var htmlContent string
			var target string
			var err error

			if len(args) == 0 {
				if isStdinTerminal() {
					return cmd.Help()
				}
				htmlContent, target, err = readFromStdin()
				if err != nil {
					return err
				}
			} else {
				target = args[0]
				slog.Debug("Fetching HTML", "target", target)
				htmlContent, err = fetchHTML(target)
				if err != nil {
					return err
				}
			}
			slog.Debug("HTML fetched", "target", target, "bytes", len(htmlContent))

			start := time.Now()
			cl, err := loadOrDownloadModel(modelPath)
			if err != nil {
				return err
			}
			slog.Debug("Model loaded", "duration", time.Since(start))

			start = time.Now()
			if proba {
				pageResult, pageErr := cl.ExtractPageTypeProba(htmlContent, threshold)
				if pageErr == nil {
					slog.Debug("Page+form classification completed", "duration", time.Since(start))
					output, _ := json.MarshalIndent(pageResult, "", "  ")
					fmt.Println(string(output))
				} else {
					results, err := cl.ExtractFormsProba(htmlContent, threshold)
					if err != nil {
						return err
					}
					slog.Debug("Form classification completed", "forms", len(results), "duration", time.Since(start))
					if len(results) == 0 {
						fmt.Println("No forms found.")
						return nil
					}
					output, _ := json.MarshalIndent(results, "", "  ")
					fmt.Println(string(output))
				}
			} else {
				pageResult, pageErr := cl.ExtractPageType(htmlContent)
				if pageErr == nil {
					slog.Debug("Page+form classification completed", "duration", time.Since(start))
					output, _ := json.MarshalIndent(pageResult, "", "  ")
					fmt.Println(string(output))
				} else {
					results, err := cl.ExtractForms(htmlContent)
					if err != nil {
						return err
					}
					slog.Debug("Form classification completed", "forms", len(results), "duration", time.Since(start))
					if len(results) == 0 {
						fmt.Println("No forms found.")
						return nil
					}
					output, _ := json.MarshalIndent(results, "", "  ")
					fmt.Println(string(output))
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&modelPath, "model", "", "Path to model file (default: auto-detect or download)")
	cmd.Flags().Float64Var(&threshold, "threshold", 0.05, "Minimum probability threshold")
	cmd.Flags().BoolVar(&proba, "proba", false, "Show probabilities")
	return cmd
}

func isStdinTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func loadOrDownloadModel(modelPath string) (*dit.Classifier, error) {
	if modelPath != "" {
		slog.Debug("Loading custom model", "path", modelPath)
		return dit.Load(modelPath)
	}

	cl, err := dit.New()
	if err == nil {
		return cl, nil
	}

	dest := filepath.Join(dit.ModelDir(), "model.json")
	slog.Info("Model not found, downloading", "url", modelURL, "dest", dest)

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return nil, fmt.Errorf("create model dir: %w", err)
	}

	resp, err := http.Get(modelURL)
	if err != nil {
		return nil, fmt.Errorf("download model: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download model: HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return nil, fmt.Errorf("create model file: %w", err)
	}

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(dest)
		return nil, fmt.Errorf("download model: %w", err)
	}
	_ = f.Close()

	slog.Info("Model downloaded", "size", fmt.Sprintf("%.1fMB", float64(written)/1024/1024))
	return dit.Load(dest)
}

func fetchHTML(target string) (string, error) {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		resp, err := http.Get(target)
		if err != nil {
			return "", fmt.Errorf("fetch URL: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("read response: %w", err)
		}
		return string(body), nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(data), nil
}

func readFromStdin() (string, string, error) {
	slog.Debug("Reading from stdin")
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", "", fmt.Errorf("read stdin: %w", err)
	}
	content := strings.TrimSpace(string(body))
	if content == "" {
		return "", "", fmt.Errorf("stdin is empty")
	}

	if strings.HasPrefix(content, "http://") || strings.HasPrefix(content, "https://") {
		slog.Debug("Stdin contains URL", "url", content)
		html, err := fetchHTML(content)
		if err != nil {
			return "", "", err
		}
		return html, content, nil
	}

	return content, "stdin", nil
}
