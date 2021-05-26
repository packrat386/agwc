package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
)

func main() {
	var queryAddress string
	flag.StringVar(&queryAddress, "address", "", "the address at which to see the weather")
	flag.Parse()

	fmt.Println("querying: ", queryAddress)

	coordinates, err := getAddressCoordinates(queryAddress)
	if err != nil {
		panic(err)
	}

	fmt.Println("lat: ", coordinates.latitude)
	fmt.Println("long: ", coordinates.longitude)

	forecastGridDataURL, err := getForecastGridDataURL(coordinates)
	if err != nil {
		panic(err)
	}

	fmt.Println("forecastGridDataURL: ", forecastGridDataURL)

	weatherData, err := getWeatherData(forecastGridDataURL, []string{"probabilityOfPrecipitation", "temperature"})
	if err != nil {
		panic(err)
	}

	display(weatherData)
}

type coordinates struct {
	latitude  float64
	longitude float64
}

func getAddressCoordinates(queryAddress string) (coordinates, error) {
	queryURL := &url.URL{
		Scheme: "https",
		Host:   "geocoding.geo.census.gov",
		Path:   "/geocoder/locations/onelineaddress",
		RawQuery: url.Values{
			"format":    []string{"json"},
			"benchmark": []string{"Public_AR_Current"},
			"address":   []string{queryAddress},
		}.Encode(),
	}

	req, err := http.NewRequest("GET", queryURL.String(), nil)
	if err != nil {
		return coordinates{}, fmt.Errorf("could not initialize HTTP request: %w", err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return coordinates{}, fmt.Errorf("could not execute HTTP request: %w", err)
	}

	body := struct {
		Result struct {
			AddressMatches []struct {
				Coordinates struct {
					X float64 `json:"x"`
					Y float64 `json:"y"`
				} `json:"coordinates"`
			} `json:"address_matches'`
		} `json: "result"`
	}{}

	err = json.NewDecoder(res.Body).Decode(&body)
	if err != nil {
		return coordinates{}, fmt.Errorf("could not parse HTTP response body: %w", err)
	}

	if len(body.Result.AddressMatches) == 0 {
		return coordinates{}, fmt.Errorf("no matching coordinates for address")
	}

	return coordinates{
		latitude:  body.Result.AddressMatches[0].Coordinates.Y,
		longitude: body.Result.AddressMatches[0].Coordinates.X,
	}, nil
}

func getForecastGridDataURL(c coordinates) (string, error) {
	queryURL := &url.URL{
		Scheme: "https",
		Host:   "api.weather.gov",
		Path:   fmt.Sprintf("/points/%f,%f", c.latitude, c.longitude),
	}

	req, err := http.NewRequest("GET", queryURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("could not initialize HTTP request: %w", err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not execute HTTP request: %w", err)
	}

	body := struct {
		Properties struct {
			ForecastGridData string `json:forecast_grid_data"`
		} `json:"properties"`
	}{}

	err = json.NewDecoder(res.Body).Decode(&body)
	if err != nil {
		return "", fmt.Errorf("could not parse HTTP response body: %w", err)
	}

	return body.Properties.ForecastGridData, nil
}

type weatherData struct {
	Properties map[string]weatherProperty `json:"properties"`
}

type weatherProperty struct {
	UnitOfMeasurement string         `json:"uom"`
	Values            []weatherPoint `json:"values"`
}

type weatherPoint struct {
	ValidTime string  `json:"validTime"`
	Value     float64 `json:"value"`
}

func getWeatherData(forecastGridDataURL string, requestedProperties []string) (map[string]weatherProperty, error) {
	req, err := http.NewRequest("GET", forecastGridDataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not initialize HTTP request: %w", err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not execute HTTP request: %w", err)
	}

	body := struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}{}

	err = json.NewDecoder(res.Body).Decode(&body)
	if err != nil {
		return nil, fmt.Errorf("could not parse HTTP response body: %w", err)
	}

	properties := map[string]weatherProperty{}

	for _, name := range requestedProperties {
		p := weatherProperty{}

		data := body.Properties[name]
		if data == nil {
			return nil, fmt.Errorf("no data for requested property: %s", name)
		}

		err := json.Unmarshal(data, &p)
		if err != nil {
			return nil, fmt.Errorf("error parsing requested property '%s': %w", name, err)
		}

		properties[name] = p
	}

	return properties, nil
}

func display(weatherData map[string]weatherProperty) {
	for name, prop := range weatherData {
		fmt.Println("----------------------------------------------")
		fmt.Println(name)
		fmt.Println("----------------------------------------------")

		for _, pt := range prop.Values {
			fmt.Printf("%40s | %06.2f | %s\n", pt.ValidTime, pt.Value, prop.UnitOfMeasurement)
		}
	}
}
