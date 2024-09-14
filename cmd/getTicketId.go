package cmd

import (
	"github.com/charmbracelet/log"
	. "ops/pkg/utils"

	"github.com/spf13/cobra"
)

var getTicketIdCmd = &cobra.Command{
	Use:   "get-ticket-id",
	Short: "Extracts the Ticket ID from the current git ref (if matches convention)",
	Run: func(cmd *cobra.Command, args []string) {
		log.Info(GetTicketId(CurrentBranch()))
	},
}

func init() {
	rootCmd.AddCommand(getTicketIdCmd)
}
