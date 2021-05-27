package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	var (
		queryAddress string
		properties   string
	)

	flag.StringVar(&queryAddress, "address", "", "the address at which to see the weather")
	flag.StringVar(&properties, "properties", "temperature", "the weather properties to display, comma separated string")
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

	weatherData, err := getWeatherData(forecastGridDataURL, strings.Split(properties, ","))
	if err != nil {
		panic(err)
	}

	display(weatherData, strings.Split(properties, ","), time.Now(), time.Now().Add(12*time.Hour))
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

type weatherPoint struct {
	StartTime time.Time
	EndTime   time.Time
	Value     float64
	Unit      unit
}

type unit string

func (u unit) String() string {
	return string(u)
}

func getWeatherData(forecastGridDataURL string, requestedProperties []string) (map[string][]weatherPoint, error) {
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

	properties := map[string][]weatherPoint{}

	for _, name := range requestedProperties {
		raw := struct {
			UnitOfMeasurement string `json:"uom"`
			Values            []struct {
				ValidTime string  `json:"validTime"`
				Value     float64 `json:"value"`
			} `json:"values"`
		}{}

		data := body.Properties[name]
		if data == nil {
			return nil, fmt.Errorf("no data for requested property: %s", name)
		}

		err := json.Unmarshal(data, &raw)
		if err != nil {
			return nil, fmt.Errorf("error parsing requested property '%s': %w", name, err)
		}

		uom, err := parseUnitOfMeasurement(raw.UnitOfMeasurement)
		if err != nil {
			return nil, fmt.Errorf("could not parse unit: %w", err)
		}

		points := []weatherPoint{}

		for _, v := range raw.Values {
			start, end, err := parseTimeRange(v.ValidTime)
			if err != nil {
				return nil, fmt.Errorf("error parsing time range: %w", err)
			}

			points = append(points, weatherPoint{
				Unit:      uom,
				StartTime: start,
				EndTime:   end,
				Value:     v.Value,
			})
		}

		// I don't know that the API is always guaranteed to return in order
		sort.Slice(points, func(i, j int) bool { return points[i].StartTime.Before(points[j].EndTime) })

		properties[name] = points
	}

	return properties, nil
}

type displayRow struct {
	at     time.Time
	values []string
}

func display(weatherData map[string][]weatherPoint, properties []string, start time.Time, end time.Time) {
	idx := map[string]int{}
	for _, p := range properties {
		idx[p] = 0
	}

	start = start.Truncate(time.Hour)
	end = end.Truncate(time.Hour)

	if start.After(end) {
		panic("display start time after end time")
	}

	rows := []displayRow{}

	for curr := start; !curr.After(end); curr = curr.Add(time.Hour) {
		// fmt.Println("DEBUG: TIME: ", curr)

		row := displayRow{
			at:     curr,
			values: []string{},
		}

		for _, property := range properties {
			// fmt.Println("DEBUG: PROP: ", property)
			points := weatherData[property]
			for idx[property] < len(points) {
				p := points[idx[property]]
				cmp := compareTimeToRange(curr, p.StartTime, p.EndTime)

				// fmt.Println("p: ", p)
				// fmt.Println("idx: ", idx[property])
				// fmt.Println("cmp: ", cmp)

				if cmp == 0 {
					row.values = append(row.values, fmt.Sprintf("%05.2f %s", p.Value, p.Unit))
					break
				}

				if cmp < 0 {
					row.values = append(row.values, "No Data")
					break
				}

				idx[property]++
			}
		}

		rows = append(rows, row)
	}

	fmt.Printf("time")
	for _, p := range properties {
		fmt.Printf(" | %s", p)
	}

	fmt.Printf("\n")
	fmt.Printf("------------------\n")

	for _, r := range rows {
		fmt.Printf(r.at.Format(time.Stamp))
		for _, v := range r.values {
			fmt.Printf(" | %s", v)
		}

		fmt.Printf("\n")
	}
}

// negative if test is before start
// positive if test is after or equal to end
// zero if test is within the range
func compareTimeToRange(test, start, end time.Time) int {
	// we don't care about monotonic clock readings for this purpose
	test = test.Round(0)
	start = start.Round(0)
	end = end.Round(0)

	if test.Before(start) {
		return -1
	}

	if test.Equal(end) || test.After(end) {
		return 1
	}

	return 0
}

func parseUnitOfMeasurement(unitOfMeasurement string) (unit, error) {
	// this is going to do more eventually
	// so sue me sandi metz

	return unit(unitOfMeasurement), nil
}

func parseTimeRange(validTime string) (time.Time, time.Time, error) {
	split := strings.Split(validTime, "/")

	if len(split) != 2 {
		return time.Time{}, time.Time{}, fmt.Errorf("malformed time + duration: %s", validTime)
	}

	start, err := time.Parse(time.RFC3339, split[0])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("could not parse time '%s': %w", split[0], err)
	}

	dur, err := parseISO8601Duration(split[1])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("could not parse duration '%s' : %w", split[1], err)
	}

	return start, applyISO8601Duration(start, dur), nil
}

type iso8601Duration struct {
	years   int64
	months  int64
	days    int64
	hours   int64
	minutes int64
	seconds int64
}

var iso8601DurationMatcher = regexp.MustCompile(`^P((?P<numYears>\d+)Y)?((?P<numMonths>\d+)M)?((?P<numDays>\d+)D)?(T((?P<numHours>\d+)H)?((?P<numMinutes>\d+)M)?((?P<numSeconds>\d+)S)?)?$|^P(?P<numWeeks>\d+)W$`)

func parseISO8601Duration(s string) (iso8601Duration, error) {
	matches := iso8601DurationMatcher.FindStringSubmatch(s)

	if len(matches) == 0 {
		return iso8601Duration{}, fmt.Errorf("'%s' is not a valid iso8601 duration", s)
	}

	yearstr := matches[iso8601DurationMatcher.SubexpIndex("numYears")]
	monthstr := matches[iso8601DurationMatcher.SubexpIndex("numMonths")]
	daystr := matches[iso8601DurationMatcher.SubexpIndex("numDays")]
	hourstr := matches[iso8601DurationMatcher.SubexpIndex("numHours")]
	minutestr := matches[iso8601DurationMatcher.SubexpIndex("numMinutes")]
	secondstr := matches[iso8601DurationMatcher.SubexpIndex("numSeconds")]

	weekstr := matches[iso8601DurationMatcher.SubexpIndex("numWeeks")]

	if weekstr != "" {
		weeks, err := strconv.ParseInt(weekstr, 10, 64)
		if err != nil {
			return iso8601Duration{}, fmt.Errorf("could not parse week value '%s' to int: %w", weekstr, err)
		}

		return iso8601Duration{days: 7 * weeks}, nil
	}

	duration := iso8601Duration{}
	var err error

	if yearstr != "" {
		duration.years, err = strconv.ParseInt(yearstr, 10, 64)
		if err != nil {
			return iso8601Duration{}, fmt.Errorf("could not parse year value '%s' to int: %w", yearstr, err)
		}
	}

	if monthstr != "" {
		duration.months, err = strconv.ParseInt(monthstr, 10, 64)
		if err != nil {
			return iso8601Duration{}, fmt.Errorf("could not parse month value '%s' to int: %w", monthstr, err)
		}
	}

	if daystr != "" {

		duration.days, err = strconv.ParseInt(daystr, 10, 64)
		if err != nil {
			return iso8601Duration{}, fmt.Errorf("could not parse day value '%s' to int: %w", daystr, err)
		}
	}

	if hourstr != "" {
		duration.hours, err = strconv.ParseInt(hourstr, 10, 64)
		if err != nil {
			return iso8601Duration{}, fmt.Errorf("could not parse hour value '%s' to int: %w", hourstr, err)
		}
	}

	if minutestr != "" {
		duration.minutes, err = strconv.ParseInt(minutestr, 10, 64)
		if err != nil {
			return iso8601Duration{}, fmt.Errorf("could not parse minute value '%s' to int: %w", minutestr, err)
		}
	}

	if secondstr != "" {
		duration.seconds, err = strconv.ParseInt(secondstr, 10, 64)
		if err != nil {
			return iso8601Duration{}, fmt.Errorf("could not parse second value '%s' to int: %w", secondstr, err)
		}
	}

	return duration, nil
}

func applyISO8601Duration(t time.Time, d iso8601Duration) time.Time {
	return t.AddDate(int(d.years), int(d.months), int(d.days)).Add(time.Duration(d.hours) * time.Hour).Add(time.Duration(d.minutes) * time.Minute).Add(time.Duration(d.seconds) * time.Second)
}
