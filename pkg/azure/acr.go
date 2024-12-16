package azure

import (
	"ops/pkg/utils"

	"github.com/charmbracelet/log"
)

func ACRLogin() {
  utils.CheckBinary("az")
  log.Warn("[WIP] az container-registry has not yet been implemented.")
}
