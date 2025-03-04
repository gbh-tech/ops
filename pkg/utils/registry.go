package utils

func GetFullRegistryRepositoryURL(registryURL string, env string, project string) string {
	return registryURL + "/" + env + "/" + project
}
