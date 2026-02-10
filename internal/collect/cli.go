package collect

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

// CLI encapsulates the dit-collect command-line interface.
type CLI struct {
	version string
	verbose bool
	rootCmd *cobra.Command
}

// New creates a new CLI instance with the given version string.
func New(version string) *CLI {
	c := &CLI{version: version}
	c.setupCommands()
	return c
}

func (c *CLI) setupCommands() {
	c.rootCmd = &cobra.Command{
		Use:     "dit-collect",
		Short:   "Collect HTML pages for page type classifier training",
		Version: c.version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			c.initLogging()
		},
	}

	c.rootCmd.PersistentFlags().BoolVarP(&c.verbose, "verbose", "v", false, "Verbose output")

	c.rootCmd.AddCommand(c.newCollectCommand())
	c.rootCmd.AddCommand(c.newCrawlCommand())
	c.rootCmd.AddCommand(c.newGenSeedCommand())
}

// Run executes the CLI and returns any error.
func (c *CLI) Run() error {
	return c.rootCmd.Execute()
}

func (c *CLI) initLogging() {
	if c.verbose {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
}
