package aws

import (
	"ops/pkg/k8s"
	"ops/pkg/utils"
)

func EKSLogin(clusterName string) {
	utils.CheckBinary("kubectl")

	utils.GetEnvironment("AWS_PROFILE")
	awsRegion := utils.GetEnvironment("AWS_REGION")

	if k8s.CredentialsRequired(clusterName) {
		k8s.UpdateConfigForEKS(
			awsRegion,
			clusterName,
		)
	}
}
