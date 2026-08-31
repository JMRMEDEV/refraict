package cli

import (
	"encoding/json"
	"fmt"

	"github.com/refraict/refraict/internal/cache"
	"github.com/refraict/refraict/internal/config"
	"github.com/spf13/cobra"
)

// resolveCacheDir loads the active config (or default) and returns the cache
// directory used by the analyze pipeline, so `cache status`/`clear` operate on
// the same on-disk cache that `analyze` writes to. See QA finding B1.
func resolveCacheDir() (string, error) {
	cfg := config.Default()
	if configPath != "" {
		c, err := config.Load(configPath)
		if err != nil {
			return "", err
		}
		cfg = c
	}
	return cacheDir(resolveCacheDB(cfg.Cache.Dir)), nil
}

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect or clear the analysis cache",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Show cache status",
			RunE: func(cmd *cobra.Command, args []string) error {
				dir, err := resolveCacheDir()
				if err != nil {
					return err
				}
				c, err := cache.New(dir, true)
				if err != nil {
					return err
				}
				st, err := c.StatusReport()
				if err != nil {
					return err
				}
				if flagJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					return enc.Encode(st)
				}
				fmt.Printf("cache entries: %d\nroot: %s\n", st.Entries, st.Root)
				return nil
			},
		},
		&cobra.Command{
			Use:   "clear",
			Short: "Clear the analysis cache",
			RunE: func(cmd *cobra.Command, args []string) error {
				dir, err := resolveCacheDir()
				if err != nil {
					return err
				}
				c, err := cache.New(dir, true)
				if err != nil {
					return err
				}
				if err := c.Clear(); err != nil {
					return err
				}
				fmt.Println("cache cleared")
				return nil
			},
		},
	)
	return cmd
}
