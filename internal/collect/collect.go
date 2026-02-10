package collect

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func (c *CLI) newCollectCommand() *cobra.Command {
	var (
		outputDir  string
		seedFile   string
		timeout    int
		delay      int
		userAgent  string
		maxPages   int
		mangleOnly bool
	)

	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Fetch pages from seed URLs and save to data/pages/",
		Example: `  dit-collect collect --seed seeds.jsonl --output data/pages
  dit-collect collect --seed seeds.jsonl --output data/pages --mangle-only`,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().IntVar(&maxPages, "max", 0, "Max pages to collect (0=unlimited)")
	cmd.Flags().BoolVar(&mangleOnly, "mangle-only", false, "Only collect mangled URLs")
	_ = cmd.MarkFlagRequired("seed")
	return cmd
}
