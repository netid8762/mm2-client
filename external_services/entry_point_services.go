package external_services

import (
	"github.com/kpango/glg"
	"mm2_client/constants"
)

func LaunchPriceServices() {
	glg.Info("Starting binance websocket service")
	go StartBinanceWebsocketService()
	glg.Info("Start forex service")
	go StartForexService()
	glg.Info("Starting coingecko price service")
	go StartCoingeckoService()
	glg.Info("Starting coinpaprika price service")
	go StartCoinpaprikaService()
	if constants.GLcwApiKey != "" {
		glg.Info("Start LiveCoinWatch service")
		go StartLcwService()
	}
	glg.Info("Start Swapscope service")
	go StartSwapscopeService()
	constants.GPricesServicesRunning = true
}

func LaunchServices(kind string, target string) {
	glg.Info("Launching price services")
	LaunchPriceServices()
}
