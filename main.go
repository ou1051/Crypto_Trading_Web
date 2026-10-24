package main
import (
	"fmt"
	"gotrading/config"
	"gotrading/utils"
	"log"
)

func main() {
	utils.LoggingSettings(config.Config.LogFile)
	log.Println("Logging settings initialized.")
	fmt.Println(config.Config.ApiKey)
	fmt.Println(config.Config.ApiSecret)
	
}