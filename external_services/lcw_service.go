package external_services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mm2_client/config"
	"mm2_client/constants"
	"mm2_client/helpers"
	"net/http"
	"sort"
	"sync"
	"time"
	"github.com/kpango/glg"
)

type LcwRequest struct {
	Currency string   `json:"currency"`
	Sort     string   `json:"sort"`
	Order    string   `json:"order"`
	Offset   float64  `json:"offset"`
	Limit    float64  `json:"limit"`
	Meta     bool     `json:"meta"`
	Codes    []string `json:"codes"`
}

type LcwAnswer struct {
	Code   string  `json:"code"`
	Rate   float64 `json:"rate"`
	Volume float64 `json:"volume"`
	Cap    float64 `json:"cap"`
	Delta  *struct {
		Hour    float64 `json:"hour"`
		Day     float64 `json:"day"`
		Week    float64 `json:"week"`
		Month   float64 `json:"monnth"`
		Quarter float64 `json:"quarter"`
		Year    float64 `json:"year"`
	} `json:"delta"`
}

const gLcwEndpoint = "https://api.livecoinwatch.com/coins/map"

var LcwPriceRegistry sync.Map

func addHeaders(r *http.Request) {
	r.Header.Add("x-api-key", constants.GLcwApiKey)
	r.Header.Add("content-type", "application/json")
}

func processLcw() *[]LcwAnswer {
	all_coins := getLwcCoinsList()
	// endpoint returns error if > 90 coins, so need to chunk it.
	fmt.Printf("LCW coins_count = %d\n", len(all_coins))
	coin_chunks := helpers.ChunkStringList(all_coins, 90)
	var answer = &[]LcwAnswer{}
	for _, coins := range coin_chunks {
		url := gLcwEndpoint
		_ = glg.Infof("Processing Lcw request: %s", url)
		req_data := LcwRequest{
			Currency: "USD",
			Sort:     "rank",
			Order:    "ascending",
			Offset:   0,
			Limit:    0,
			Codes:    coins,
			Meta:     false,
		}
		json_body := new(bytes.Buffer)
		json.NewEncoder(json_body).Encode(req_data)

                req, err := http.NewRequest("POST", url, json_body)
                if err != nil {
                    glg.Errorf("Could not create Lcw request: %v", err)
                    time.Sleep(10 * time.Second)
                    continue
                }
                addHeaders(req)

                client := &http.Client{Timeout: 20 * time.Second}
                resp, err := client.Do(req)
                if err != nil {
                    // A response can accompany some errors (for example, redirect errors).
                    if resp != nil && resp.Body != nil {
                        _ = resp.Body.Close()
                    }
                    glg.Errorf("Lcw request failed: %v", err)
                    time.Sleep(10 * time.Second)
                    continue
                }

                func() {
                    defer resp.Body.Close()

                    if resp.StatusCode == http.StatusOK {
                        chunkAnswer := &[]LcwAnswer{}
                        if decodeErr := json.NewDecoder(resp.Body).Decode(chunkAnswer); decodeErr != nil {
                            glg.Errorf("Could not decode Lcw response: %v", decodeErr)
                            return
                        }
                        if len(*chunkAnswer) > 0 {
                            *answer = append(*answer, *chunkAnswer...)
                        }
                        return
                    }

                    bodyBytes, readErr := ioutil.ReadAll(resp.Body)
                    if readErr != nil {
                        glg.Errorf("Could not read Lcw error response: %v", readErr)
                        return
                    }
                    glg.Errorf("Lcw returned HTTP status %s: %s", resp.Status, bodyBytes)
                }()

                time.Sleep(10 * time.Second)
	}
	if len(*answer) > 0 {
		return answer
	} else {
		return nil
	}
}

func StartLcwService() {
	for {
		if resp := processLcw(); resp != nil {
			glg.Info("Lcw request successfully processed")
			for _, cur := range *resp {
				LcwPriceRegistry.Store(cur.Code, cur)
			}
		} else {
			glg.Error("Something went wrong when processing Lcw request")
		}
		time.Sleep(constants.GPricesLoopTime)
	}
}

func LcwRetrieveUSDValIfSupported(coin string) (string, string, string) {
	dateStr := helpers.GetDateFromTimestampStandard(time.Now().UnixNano())
	valStr := "0"
	if cfg, cfgOk := config.GCFGRegistry[coin]; cfgOk {
		val, ok := LcwPriceRegistry.Load(cfg.LcwId)
		if ok {
			resp := val.(LcwAnswer)
			valStr = fmt.Sprintf("%.10f", resp.Rate)
		}
		return valStr, dateStr, "livecoinwatch"
	}
	return valStr, dateStr, "unknown"
}

func LcwRetrieveCEXRatesFromPair(base string, rel string) (string, bool, string, string) {
	basePrice, baseDate, _ := LcwRetrieveUSDValIfSupported(base)
	relPrice, _, _ := LcwRetrieveUSDValIfSupported(rel)
	price := helpers.BigFloatDivide(basePrice, relPrice, 8)
	return price, true, baseDate, "livecoinwatch"
}

func LcwGetTotalVolume(coin string) (string, string, string) {
	totalVolumeStr := "0"
	dateStr := helpers.GetDateFromTimestampStandard(time.Now().UnixNano())
	if cfg, cfgOk := config.GCFGRegistry[coin]; cfgOk {
		val, ok := LcwPriceRegistry.Load(cfg.LcwId)
		if ok {
			resp := val.(LcwAnswer)
			totalVolumeStr = fmt.Sprintf("%.10f", resp.Volume)
		}
		return totalVolumeStr, dateStr, "livecoinwatch"
	}
	return totalVolumeStr, dateStr, "unknown"
}

func LcwGetChange24h(coin string) (string, string, string) {
	changePercent24h := "0"
	dateStr := helpers.GetDateFromTimestampStandard(time.Now().UnixNano())
	if cfg, cfgOk := config.GCFGRegistry[coin]; cfgOk {
		val, ok := LcwPriceRegistry.Load(cfg.LcwId)
		if ok {
			resp := val.(LcwAnswer)
			changePercent24h = fmt.Sprintf("%.10f", resp.Delta.Day)
		}
		return changePercent24h, dateStr, "livecoinwatch"
	}
	return changePercent24h, dateStr, "unknown"
}

func getLwcCoinsList() []string {
	coins := []string{}
	for _, cur := range config.GCFGRegistry {
		if cur.LcwId != "test-coin" && cur.LcwId != "" {
			coins = append(coins, cur.LcwId)
		}
	}
	coins = helpers.UniqueStrings(coins)
	sort.Strings(coins)
	return coins
}
