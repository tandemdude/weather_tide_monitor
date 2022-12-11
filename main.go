package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// --- HTTP REQUEST PAYLOADS ---
// -- WEATHER --
type HourlyWeather struct {
	TempC          string `json:"tempC"`
	Time           string `json:"time"`
	WeatherCode    string `json:"weatherCode"`
	WindDir16Point string `json:"winddir16Point"`
	WindSpeedKmph  string `json:"windspeedKmph"`
}

type WeatherDay struct {
	AvgTempC string          `json:"avgtempC"`
	MaxTempC string          `json:"maxtempC"`
	MinTempC string          `json:"mintempC"`
	Date     string          `json:"date"`
	Hourly   []HourlyWeather `json:"hourly"`
}

type WeatherResponse struct {
	Weather []WeatherDay `json:"weather"`
}

// -- TIDES --
type TideExtreme struct {
	Time string `json:"Time"`
	Type uint8  `json:"Type"`
}

type TideTable struct {
	Name string                   `json:"name"`
	Rows map[string][]TideExtreme `json:"rows"`
}

type TideResponse struct {
	Month string               `json:"month"`
	Table map[string]TideTable `json:"table"`
}

// --- RETURNED PAYLOAD ---
type WeatherPeriodData struct {
	WeatherType int16 `json:"weather_type"`
	Temperature int16 `json:"temperature"`
}

type TidesData struct {
	FirstTide uint8    `json:"first_tide"`
	Times     []string `json:"times"`
}

type WindData struct {
	Direction string `json:"direction"`
	Strength  string `json:"strength"`
}

type LambdaResponse struct {
	Date           string              `json:"date"`
	WeatherPeriods []WeatherPeriodData `json:"weather_periods"`
	Tides          TidesData           `json:"tides"`
	Wind           WindData            `json:"wind"`
	Message        string              `json:"message"`
}

// --- LOGIC ---
func WindSpeedText(speed int16) string {
	if speed < 2 {
		return "calm"
	} else if speed < 6 {
		return "light air"
	} else if speed < 12 {
		return "light breeze"
	} else if speed < 20 {
		return "gentle breeze"
	} else if speed < 30 {
		return "moderate breeze"
	} else if speed < 40 {
		return "fresh breeze"
	} else if speed < 50 {
		return "strong"
	} else if speed < 62 {
		return "near gale"
	} else if speed < 75 {
		return "gale"
	} else if speed < 89 {
		return "strong gale"
	} else if speed < 103 {
		return "storm"
	} else if speed < 118 {
		return "violent storm"
	}
	return "hurricane"
}

func GetData[T WeatherResponse | TideResponse](url string, data *T) error {
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode > 299 {
		return errors.New("error making http request")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.New("error reading response body")
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return err
	}
	return nil
}

func HandleLambdaEvent() LambdaResponse {
	currentTime := time.Now().UTC()
	weatherIndex := 0

	if currentTime.Hour() >= 12 {
		currentTime = currentTime.AddDate(0, 0, 1)
		currentTime = currentTime.Add(time.Hour * time.Duration(currentTime.Hour()) * -1)
		weatherIndex = 1
	}

	lowTideOffset, err := strconv.Atoi(os.Getenv("LOW_TIDE_OFFSET"))
	if err != nil {
		log.Fatalln(err)
	}
	highTideOffset, err := strconv.Atoi(os.Getenv("HIGH_TIDE_OFFSET"))
	if err != nil {
		log.Fatalln(err)
	}
	tideUrl := fmt.Sprintf(
		"https://tidepredictions.pla.co.uk/gauge_data/0113/%s/%s/%s/0/1/",
		currentTime.Format("2006"), currentTime.Format("01"), currentTime.Format("02"),
	)

	var tideData TideResponse
	if err := GetData(tideUrl, &tideData); err != nil {
		return LambdaResponse{
			Message: "Could not fetch tide data",
		}
	}
	log.Println("Tide response fetched successfully")
	var weatherData WeatherResponse
	if err := GetData("https://wttr.in/Mortlake?format=j1", &weatherData); err != nil {
		return LambdaResponse{
			Message: "Could not fetch weather data",
		}
	}
	log.Println("Weather response fetched successfully")

	tideTimes := make([]string, 4)
	for i, tide := range tideData.Table["0"].Rows[strconv.Itoa(currentTime.Day()-1)] {
		parsed, err := time.Parse("2006-01-02 15:04", currentTime.Format("2006-01-02 ")+tide.Time[:2]+":"+tide.Time[2:])
		if err != nil {
			return LambdaResponse{Message: "Parser failure"}
		}

		if tide.Type == 0 {
			tideTimes[i] = parsed.Add(time.Minute * time.Duration(lowTideOffset)).Format("15:04")
		} else {
			tideTimes[i] = parsed.Add(time.Minute * time.Duration(highTideOffset)).Format("15:04")
		}
	}

	var currentWeather HourlyWeather
	dayWeather := make([]WeatherPeriodData, len(weatherData.Weather[weatherIndex].Hourly))
	for i, weather := range weatherData.Weather[weatherIndex].Hourly {
		hourTime, err := strconv.ParseInt(weather.Time, 10, 16)
		if err != nil {
			return LambdaResponse{Message: "Parser failure"}
		}
		currTime, err := strconv.ParseInt(currentTime.Format("1504"), 10, 16)
		if err != nil {
			return LambdaResponse{Message: "Parser failure"}
		}

		if currTime >= hourTime {
			currentWeather = weather
		}

		weatherType, err := strconv.ParseInt(weather.WeatherCode, 10, 16)
		if err != nil {
			return LambdaResponse{Message: "Parser failure"}
		}
		temperature, err := strconv.ParseInt(weather.TempC, 10, 16)
		if err != nil {
			return LambdaResponse{Message: "Parser failure"}
		}

		dayWeather[i] = WeatherPeriodData{
			WeatherType: int16(weatherType),
			Temperature: int16(temperature),
		}
	}

	currWindSpeed, err := strconv.ParseInt(currentWeather.WindSpeedKmph, 10, 16)
	if err != nil {
		return LambdaResponse{Message: "Parser failure"}
	}

	return LambdaResponse{
		Date: currentTime.Format("2006-01-02"),
		Tides: TidesData{
			FirstTide: tideData.Table["0"].Rows[strconv.Itoa(currentTime.Day()-1)][0].Type,
			Times:     tideTimes,
		},
		Wind: WindData{
			Direction: currentWeather.WindDir16Point,
			Strength:  WindSpeedText(int16(currWindSpeed)),
		},
		WeatherPeriods: dayWeather,
	}
}

func main() {
	lambda.Start(HandleLambdaEvent)
}

// Tides for london bridge -> chiswick bridge - HW = +46m, LW = +144m
// Tides for london bridge https://tidepredictions.pla.co.uk/gauge_data/0113/2022/12/6/0/1/
