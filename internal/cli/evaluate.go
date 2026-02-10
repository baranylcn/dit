package cli

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/happyhackingspace/dit"
	"github.com/spf13/cobra"
)

func (c *CLI) newEvaluateCommand() *cobra.Command {
	var dataFolder string
	var cvFolds int

	cmd := &cobra.Command{
		Use:     "evaluate",
		Short:   "Evaluate model accuracy via cross-validation",
		Example: `  dit evaluate --data-folder data --cv 10`,
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.Info("Evaluating", "folds", cvFolds, "data-folder", dataFolder)
			start := time.Now()
			result, err := dit.Evaluate(dataFolder, &dit.EvalConfig{
				Folds:   cvFolds,
				Verbose: c.verbose,
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

	cmd.Flags().StringVar(&dataFolder, "data-folder", "data", "Path to annotation data folder")
	cmd.Flags().IntVar(&cvFolds, "cv", 10, "Number of cross-validation folds")
	return cmd
}

func printClassReport(confusion map[string]map[string]int, classes []string, precision, recall, f1 map[string]float64) {
	fmt.Printf("\nPer-class metrics:\n")
	fmt.Printf("%8s  %6s  %6s  %6s  %7s\n", "class", "prec", "recall", "f1", "support")
	for _, cls := range classes {
		support := 0
		for _, v := range confusion[cls] {
			support += v
		}
		fmt.Printf("%8s  %5.1f%%  %5.1f%%  %5.1f%%  %7d\n",
			cls, precision[cls]*100, recall[cls]*100, f1[cls]*100, support)
	}
}

func printConfusionMatrix(confusion map[string]map[string]int, classes []string) {
	if len(confusion) == 0 {
		return
	}

	sort.Slice(classes, func(i, j int) bool {
		ti, tj := 0, 0
		for _, v := range confusion[classes[i]] {
			ti += v
		}
		for _, v := range confusion[classes[j]] {
			tj += v
		}
		return ti > tj
	})

	fmt.Printf("\nConfusion matrix (rows=true, cols=predicted):\n")
	fmt.Printf("%8s", "")
	for _, c := range classes {
		fmt.Printf(" %5s", c)
	}
	fmt.Printf("  total  acc%%\n")

	for _, trueClass := range classes {
		fmt.Printf("%8s", trueClass)
		total := 0
		correct := 0
		for _, predClass := range classes {
			count := confusion[trueClass][predClass]
			total += count
			if trueClass == predClass {
				correct = count
			}
			if count == 0 {
				fmt.Printf("   %5s", ".")
			} else {
				fmt.Printf("   %3d", count)
			}
		}
		acc := 0.0
		if total > 0 {
			acc = float64(correct) / float64(total) * 100
		}
		fmt.Printf("  %5d %5.1f\n", total, acc)
	}
}
