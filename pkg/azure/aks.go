package azure

import (
	"ops/pkg/k8s"
	"ops/pkg/utils"
)

func AKSLogin(clusterName string, resourceGroup string) {
	utils.CheckBinary("az")

	utils.GetEnvironment("AZURE_SUBSCRIPTION_ID")
	rg := utils.GetEnvironment("AZURE_RESOURCE_GROUP")

	if k8s.CredentialsRequired(clusterName) {
		k8s.UpdateConfigForAKS(
			clusterName,
			rg,
		)
	}
}
