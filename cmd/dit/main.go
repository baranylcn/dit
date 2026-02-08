package main

import (
<<<<<<< HEAD
=======
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
>>>>>>> d368908 (feat: Add JavaScript rendering support for SPA forms)
	"os"

<<<<<<< HEAD
	"github.com/happyhackingspace/dit/internal/cli"
=======
	"github.com/chromedp/chromedp"
	"github.com/happyhackingspace/dit"
	"github.com/spf13/cobra"
>>>>>>> d368908 (feat: Add JavaScript rendering support for SPA forms)
)

var version = "dev"

<<<<<<< HEAD
func main() {
	if err := cli.New(version).Run(); err != nil {
=======
var (
	verbose        bool
	silent         bool
	appInitialized bool
)

func initApp() {
	if appInitialized {
		return
	}
	appInitialized = true

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	if silent {
		level = slog.Level(100)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})))
	if !silent {
		fmt.Fprint(os.Stderr, banner())
	}
}

const modelURL = "https://github.com/happyhackingspace/dit/raw/main/model.json"

func loadOrDownloadModel(modelPath string) (*dit.Classifier, error) {
	if modelPath != "" {
		slog.Debug("Loading custom model", "path", modelPath)
		return dit.Load(modelPath)
	}

	c, err := dit.New()
	if err == nil {
		return c, nil
	}

	// Model not found locally — download it
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

func fetchHTMLRender(url string, timeout time.Duration) (string, error) {
	httpClient := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}

	resp, err := httpClient.Head(url)
	if err != nil {
		return "", fmt.Errorf("redirect check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	finalURL := resp.Request.URL.String()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ctx, cancel = chromedp.NewContext(ctx)
	defer cancel()

	var htmlContent string
	err = chromedp.Run(ctx,
		chromedp.Navigate(finalURL),
		chromedp.WaitReady("body"),
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return "", fmt.Errorf("render browser: %w", err)
	}

	return htmlContent, nil
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

func main() {
	rootCmd := &cobra.Command{
		Use:     "dît",
		Short:   "HTML form and field type classifier",
		Version: version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			initApp()
		},
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose/debug output")
	rootCmd.PersistentFlags().BoolVarP(&silent, "silent", "s", false, "Suppress all logging and banner")

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		initApp()
		defaultHelp(cmd, args)
	})

	var trainDataFolder string
	trainCmd := &cobra.Command{
		Use:   "train <modelfile>",
		Short: "Train a model on annotated HTML forms",
		Args:  cobra.ExactArgs(1),
		Example: `  dit train model.json --data-folder data
  dit train model.json -v`,
		RunE: func(cmd *cobra.Command, args []string) error {
			modelPath := args[0]
			slog.Info("Training classifier", "data-folder", trainDataFolder, "output", modelPath)
			start := time.Now()
			c, err := dit.Train(trainDataFolder, &dit.TrainConfig{Verbose: verbose})
			if err != nil {
				return err
			}
			slog.Debug("Training completed", "duration", time.Since(start))
			if err := c.Save(modelPath); err != nil {
				return err
			}
			slog.Info("Model saved", "path", modelPath)
			return nil
		},
	}
	trainCmd.Flags().StringVar(&trainDataFolder, "data-folder", "data", "Path to annotation data folder")

	var runModelPath string
	var runThreshold float64
	var runProba bool
	var runRender bool
	var runTimeout int
	runCmd := &cobra.Command{
		Use:   "run <url-or-file>",
		Short: "Classify page type and forms in a URL or HTML file",
		Args:  cobra.ExactArgs(1),
		Example: `  dit run https://github.com/login
  dit run login.html
  dit run https://github.com/login --proba
  dit run https://github.com/login --proba --threshold 0.1
  dit run https://github.com/login --render
  dit run https://github.com/login --model custom.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]

			start := time.Now()
			c, err := loadOrDownloadModel(runModelPath)
			if err != nil {
				return err
			}
			slog.Debug("Model loaded", "duration", time.Since(start))

			slog.Debug("Fetching HTML", "target", target, "render", runRender)
			var htmlContent string
			if runRender && (strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")) {
				htmlContent, err = fetchHTMLRender(target, time.Duration(runTimeout)*time.Second)
			} else {
				htmlContent, err = fetchHTML(target)
			}
			if err != nil {
				return err
			}
			slog.Debug("HTML fetched", "bytes", len(htmlContent))

			start = time.Now()
			if runProba {
				pageResult, pageErr := c.ExtractPageTypeProba(htmlContent, runThreshold)
				if pageErr == nil {
					slog.Debug("Page+form classification completed", "duration", time.Since(start))
					output, _ := json.MarshalIndent(pageResult, "", "  ")
					fmt.Println(string(output))
				} else {
					// Fall back to form-only classification
					results, err := c.ExtractFormsProba(htmlContent, runThreshold)
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
				pageResult, pageErr := c.ExtractPageType(htmlContent)
				if pageErr == nil {
					slog.Debug("Page+form classification completed", "duration", time.Since(start))
					output, _ := json.MarshalIndent(pageResult, "", "  ")
					fmt.Println(string(output))
				} else {
					// Fall back to form-only classification
					results, err := c.ExtractForms(htmlContent)
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
	runCmd.Flags().StringVar(&runModelPath, "model", "", "Path to model file (default: auto-detect or download)")
	runCmd.Flags().Float64Var(&runThreshold, "threshold", 0.05, "Minimum probability threshold")
	runCmd.Flags().BoolVar(&runProba, "proba", false, "Show probabilities")
	runCmd.Flags().BoolVar(&runRender, "render", false, "Use render browser for JavaScript-rendered pages")
	runCmd.Flags().IntVar(&runTimeout, "timeout", 30, "Render browser timeout in seconds")

	var evalDataFolder string
	var evalCVFolds int
	evalCmd := &cobra.Command{
		Use:     "evaluate",
		Short:   "Evaluate model accuracy via cross-validation",
		Example: `  dit evaluate --data-folder data --cv 10`,
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.Info("Evaluating", "folds", evalCVFolds, "data-folder", evalDataFolder)
			start := time.Now()
			result, err := dit.Evaluate(evalDataFolder, &dit.EvalConfig{
				Folds:   evalCVFolds,
				Verbose: verbose,
			})
			if err != nil {
				return err
			}
			slog.Debug("Evaluation completed", "duration", time.Since(start))

			if result.FormTotal > 0 {
				fmt.Printf("Form type accuracy: %.1f%% (%d/%d)\n",
					result.FormAccuracy*100, result.FormCorrect, result.FormTotal)
			}
			if result.FieldTotal > 0 {
				fmt.Printf("Field type accuracy: %.1f%% (%d/%d fields)\n",
					result.FieldAccuracy*100, result.FieldCorrect, result.FieldTotal)
				fmt.Printf("Sequence accuracy: %.1f%% (%d/%d forms)\n",
					result.SequenceAccuracy*100, result.SequenceCorrect, result.SequenceTotal)
			}
			if result.PageTotal > 0 {
				fmt.Printf("Page type accuracy: %.1f%% (%d/%d)\n",
					result.PageAccuracy*100, result.PageCorrect, result.PageTotal)
				fmt.Printf("Macro F1: %.1f%%  Weighted F1: %.1f%%\n",
					result.PageMacroF1*100, result.PageWeightedF1*100)
				printConfusionMatrix(result.PageConfusion, result.PageClasses)
				printClassReport(result.PageConfusion, result.PageClasses, result.PagePrecision, result.PageRecall, result.PageF1)
			}
			return nil
		},
	}
	evalCmd.Flags().StringVar(&evalDataFolder, "data-folder", "data", "Path to annotation data folder")
	evalCmd.Flags().IntVar(&evalCVFolds, "cv", 10, "Number of cross-validation folds")

	rootCmd.AddCommand(trainCmd, runCmd, evalCmd)

	if err := rootCmd.Execute(); err != nil {
>>>>>>> d368908 (feat: Add JavaScript rendering support for SPA forms)
		os.Exit(1)
	}
}
