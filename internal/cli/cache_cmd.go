package cli

import (
	"encoding/json"
	"fmt"

	"github.com/refraict/refraict/internal/cache"
	"github.com/spf13/cobra"
)

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
				c, err := cache.New("./.refraict-cache", true)
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
				c, err := cache.New("./.refraict-cache", true)
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
