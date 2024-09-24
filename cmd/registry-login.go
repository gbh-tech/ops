package cmd

import (
	"github.com/spf13/cobra"
	"ops/pkg/aws"
)

var registryLoginCmd = &cobra.Command{
	Use:   "registry-login",
	Short: "Logs in to the specified container image registry (ECR, ACR, etc)",
	Run: func(cmd *cobra.Command, args []string) {
		aws.ECRLogin()
	},
}

func init() {
	rootCmd.AddCommand(registryLoginCmd)
}
