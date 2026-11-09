package external_services

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mm2_client/config"
	"mm2_client/constants"
	"mm2_client/helpers"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kpango/glg"
)

const (
	gSwapscopeEndpointNetid8762 = "https://swapscope.net/api/v1/prices?netid=8762"
	gSwapscopeEndpointNetid6133 = "https://swapscope.net/api/v1/prices?netid=6133"
)

type SwapscopeAnswer struct {
	Symbol       string  `json:"symbol"`
	USD          *string `json:"usd"`
	Change24hPct *string `json:"change24hPct"`
	ComputedAt   string  `json:"computedAt"`
}

type swapscopeResponse struct {
	Data []SwapscopeAnswer `json:"data"`
}

type SwapscopeAggregatedValue struct {
	Price        float64
	Change24hPct float64
	HasPrice     bool
	HasChange    bool
	LastUpdated  string
}

type swapscopeAggregate struct {
	priceSum    float64
	priceCount  int
	changeSum   float64
	changeCount int
	lastUpdated string
}

var SwapscopePriceRegistry sync.Map

func normalizeSwapscopeTicker(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func fetchSwapscopePrices(client *http.Client, endpoint string) ([]SwapscopeAnswer, error) {
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP status %s: %s", resp.Status, bodyBytes)
	}

	var result swapscopeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func processSwapscope() map[string]SwapscopeAggregatedValue {
	client := &http.Client{Timeout: 20 * time.Second}
	aggregates := make(map[string]*swapscopeAggregate)
	successfulRequests := 0

	for _, endpoint := range []string{
		gSwapscopeEndpointNetid8762,
		gSwapscopeEndpointNetid6133,
	} {
		items, err := fetchSwapscopePrices(client, endpoint)
		if err != nil {
			glg.Errorf("Swapscope request failed for %s: %v", endpoint, err)
			continue
		}
		successfulRequests++

		for _, item := range items {
			symbol := normalizeSwapscopeTicker(item.Symbol)
			if symbol == "" {
				continue
			}

			agg, ok := aggregates[symbol]
			if !ok {
				agg = &swapscopeAggregate{}
				aggregates[symbol] = agg
			}

			if item.USD != nil {
				if value, parseErr := strconv.ParseFloat(strings.TrimSpace(*item.USD), 64); parseErr == nil {
					agg.priceSum += value
					agg.priceCount++
				}
			}

			if item.Change24hPct != nil {
				if value, parseErr := strconv.ParseFloat(strings.TrimSpace(*item.Change24hPct), 64); parseErr == nil {
					agg.changeSum += value
					agg.changeCount++
				}
			}

			// Use the oldest contributing source timestamp so validity checks
			// do not treat an average containing stale data as fully fresh.
			if item.ComputedAt != "" && (agg.lastUpdated == "" || item.ComputedAt < agg.lastUpdated) {
				agg.lastUpdated = item.ComputedAt
			}
		}
	}

	if successfulRequests == 0 {
		return nil
	}

	result := make(map[string]SwapscopeAggregatedValue, len(aggregates))
	for symbol, agg := range aggregates {
		value := SwapscopeAggregatedValue{LastUpdated: agg.lastUpdated}
		if value.LastUpdated == "" {
			value.LastUpdated = helpers.GetDateFromTimestampStandard(time.Now().UnixNano())
		}
		if agg.priceCount > 0 {
			value.Price = agg.priceSum / float64(agg.priceCount)
			value.HasPrice = true
		}
		if agg.changeCount > 0 {
			value.Change24hPct = agg.changeSum / float64(agg.changeCount)
			value.HasChange = true
		}
		if value.HasPrice || value.HasChange {
			result[symbol] = value
		}
	}
	return result
}

func StartSwapscopeService() {
	for {
		if result := processSwapscope(); result != nil {
			glg.Info("Swapscope request successfully processed")
			for symbol, value := range result {
				SwapscopePriceRegistry.Store(symbol, value)
			}
		} else {
			glg.Error("Something went wrong when processing Swapscope request")
		}
		time.Sleep(constants.GPricesLoopTime)
	}
}

func swapscopeTickerForCoin(coin string) string {
	if cfg, ok := config.GCFGRegistry[coin]; ok && cfg != nil {
		if cfg.Coin != "" {
			return normalizeSwapscopeTicker(cfg.Coin)
		}
		if cfg.AliasTicker != nil && *cfg.AliasTicker != "" {
			return normalizeSwapscopeTicker(*cfg.AliasTicker)
		}
	}
	return normalizeSwapscopeTicker(coin)
}

func loadSwapscopeValue(coin string) (SwapscopeAggregatedValue, bool) {
	key := swapscopeTickerForCoin(coin)
	value, ok := SwapscopePriceRegistry.Load(key)
	if !ok {
		// A caller may use a config key whose configured ticker differs; try
		// the key and alias before reporting that this ticker is unsupported.
		if cfg, cfgOk := config.GCFGRegistry[coin]; cfgOk && cfg != nil && cfg.AliasTicker != nil {
			value, ok = SwapscopePriceRegistry.Load(normalizeSwapscopeTicker(*cfg.AliasTicker))
		}
	}
	if !ok {
		return SwapscopeAggregatedValue{}, false
	}
	result, ok := value.(SwapscopeAggregatedValue)
	return result, ok
}

func SwapscopeRetrieveUSDValIfSupported(coin string) (string, string, string) {
	dateStr := helpers.GetDateFromTimestampStandard(time.Now().UnixNano())
	value, ok := loadSwapscopeValue(coin)
	if !ok || !value.HasPrice {
		return "0", dateStr, "unknown"
	}
	return fmt.Sprintf("%.10f", value.Price), value.LastUpdated, "swapscope"
}

func SwapscopeRetrieveCEXRatesFromPair(base string, rel string) (string, bool, string, string) {
	basePrice, baseDate, _ := SwapscopeRetrieveUSDValIfSupported(base)
	relPrice, _, _ := SwapscopeRetrieveUSDValIfSupported(rel)
	price := helpers.BigFloatDivide(basePrice, relPrice, 8)
	return price, true, baseDate, "swapscope"
}

func SwapscopeGetChange24h(coin string) (string, string, string) {
	dateStr := helpers.GetDateFromTimestampStandard(time.Now().UnixNano())
	value, ok := loadSwapscopeValue(coin)
	if !ok || !value.HasChange {
		return "0", dateStr, "unknown"
	}
	return fmt.Sprintf("%.10f", value.Change24hPct), value.LastUpdated, "swapscope"
}
