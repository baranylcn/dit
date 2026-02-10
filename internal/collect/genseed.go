package collect

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func (c *CLI) newGenSeedCommand() *cobra.Command {
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
			defer func() { _ = f.Close() }()

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
