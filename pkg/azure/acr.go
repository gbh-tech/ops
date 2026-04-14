package azure

import (
	"ops/pkg/utils"

	"charm.land/log/v2"
)

func ACRLogin() {
	utils.CheckBinary("az")
	log.Warn("[WIP] az container-registry has not yet been implemented.")
}
