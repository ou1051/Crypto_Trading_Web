package main
import (
	"fmt"
	"gotrading/config"
	"gotrading/utils"

	"gotrading/bitflyer"
)

func main() {
	utils.LoggingSettings(config.Config.LogFile)
	apiClient := bitflyer.New(config.Config.ApiKey, config.Config.ApiSecret)
	fmt.Println(apiClient.GetBalance())

	
}