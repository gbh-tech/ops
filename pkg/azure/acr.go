package azure

import (
	"github.com/charmbracelet/log"
	"ops/pkg/utils"
)

func ACRLogin() {
	utils.CheckBinary("az")
	log.Warn("[WIP] az container-registry has not yet been implemented.")
}
