package cli

import (
	"log/slog"
	"time"

	"github.com/happyhackingspace/dit"
	"github.com/spf13/cobra"
)

func (c *CLI) newTrainCommand() *cobra.Command {
	var dataFolder string

	cmd := &cobra.Command{
		Use:   "train <modelfile>",
		Short: "Train a model on annotated HTML forms",
		Args:  cobra.ExactArgs(1),
		Example: `  dit train model.json --data-folder data
  dit train model.json -v`,
		RunE: func(cmd *cobra.Command, args []string) error {
			modelPath := args[0]
			slog.Info("Training classifier", "data-folder", dataFolder, "output", modelPath)
			start := time.Now()
			cl, err := dit.Train(dataFolder, &dit.TrainConfig{Verbose: c.verbose})
			if err != nil {
				return err
			}
			slog.Debug("Training completed", "duration", time.Since(start))
			if err := cl.Save(modelPath); err != nil {
				return err
			}
			slog.Info("Model saved", "path", modelPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&dataFolder, "data-folder", "data", "Path to annotation data folder")
	return cmd
}
