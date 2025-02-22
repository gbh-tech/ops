package git

import (
	"ops/pkg/utils"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var GetTicketIDCommand = &cobra.Command{
	Use:   "get-ticket-id",
	Short: "Extracts the Ticket ID from the current git ref (if matches convention)",
	Run: func(cmd *cobra.Command, args []string) {
		log.Info(
			utils.GetTicketId(
				utils.CurrentBranch(),
			),
		)
	},
}
