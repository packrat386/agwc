package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var permittedProperties = []string{
	"dewpoint",
	"heatIndex",
	"maxTemperature",
	"minTemperature",
	"pressure",
	"probabilityOfPrecipitation",
	"probabilityOfThunder",
	"quantitativePrecipitation",
	"relativeHumidity",
	"skyCover",
	"temperature",
	"windChill",
	"windDirection",
	"windSpeed",
}

func main() {
	req, err := getForecastRequest(os.Args)
	if err != nil {
		errorAndQuit(err)
	}

	coordinates, err := getAddressCoordinates(req.address)
	if err != nil {
		errorAndQuit(err)
	}

	fmt.Println("lat: ", coordinates.latitude)
	fmt.Println("long: ", coordinates.longitude)

	forecastGridDataURL, err := getForecastGridDataURL(coordinates)
	if err != nil {
		errorAndQuit(err)
	}

	fmt.Println("forecastGridDataURL: ", forecastGridDataURL)

	weatherData, err := getWeatherData(forecastGridDataURL, req.properties)
	if err != nil {
		errorAndQuit(err)
	}

	display(req, weatherData)
}

type forecastRequest struct {
	address         string
	properties      []string
	start           time.Time
	end             time.Time
	displayTimeZone *time.Location
	freedom         bool
}

func getForecastRequest(args []string) (forecastRequest, error) {
	flagset := flag.NewFlagSet(args[0], flag.ExitOnError)

	var (
		queryAddress string
		properties   string
		hours        int
		offset       int
		displaytz    string
		freedom      bool
	)

	flagset.StringVar(&queryAddress, "address", "", "address at which to see the weather")
	flagset.StringVar(&properties, "properties", "temperature", "weather properties to display in a comma separated string")
	flagset.IntVar(&hours, "hours", 12, "number of hours of predictions to show")
	flagset.IntVar(&offset, "offset", 0, "start predictions this many hours from now")
	flagset.StringVar(&displaytz, "displaytz", "UTC", "time zone in which to display predictions")
	flagset.BoolVar(&freedom, "freedom", false, "use freedom units")

	flagset.Parse(args[1:])

	loc, err := time.LoadLocation(displaytz)
	if err != nil {
		return forecastRequest{}, fmt.Errorf("could not load display timezone: %w", err)
	}

	start := time.Now().Add(time.Duration(offset) * time.Hour)
	end := start.Add(time.Duration(hours) * time.Hour)

	req := forecastRequest{
		address:         queryAddress,
		properties:      strings.Split(properties, ","),
		start:           start,
		end:             end,
		displayTimeZone: loc,
		freedom:         freedom,
	}

	if req.address == "" {
		return forecastRequest{}, fmt.Errorf("address cannot be empty")
	}

	for _, p := range req.properties {
		if !containsString(permittedProperties, p) {
			return forecastRequest{}, fmt.Errorf("requested property '%s' is not in %v", p, permittedProperties)
		}
	}

	return req, nil
}

func containsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if needle == v {
			return true
		}
	}

	return false
}

func errorAndQuit(err error) {
	fmt.Println("agcw encountered an error: ", err.Error())
	os.Exit(1)
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
	Unit      string
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

		points := []weatherPoint{}

		for _, v := range raw.Values {
			start, end, err := parseTimeRange(v.ValidTime)
			if err != nil {
				return nil, fmt.Errorf("error parsing time range: %w", err)
			}

			points = append(points, weatherPoint{
				Unit:      raw.UnitOfMeasurement,
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

func formatWeatherValue(p weatherPoint, freedom bool) string {
	if freedom {
		p = liberate(p)
	}

	return fmt.Sprintf("%5.5g %s", p.Value, displayUnit(p.Unit))
}

func liberate(p weatherPoint) weatherPoint {
	f := weatherPoint{StartTime: p.StartTime, EndTime: p.EndTime}

	switch p.Unit {
	case "wmoUnit:degC":
		f.Value = ((p.Value * 9.0) / 5.0) + 32
		f.Unit = "F"
	case "wmoUnit:km_h-1":
		f.Value = p.Value * 0.621371
		f.Unit = "mph"
	case "wmoUnit:mm":
		f.Value = p.Value * 0.0393701
		f.Unit = "in"
	case "wmoUnit:m":
		f.Value = p.Value * 3.28084
		f.Unit = "ft"
	default:
		f.Value = p.Value
		f.Unit = p.Unit
	}

	return f
}

func displayUnit(unit string) string {
	switch unit {
	case "wmoUnit:degC":
		return "C"
	case "wmoUnit:km_h-1":
		return "kph"
	case "wmoUnit:percent":
		return "%"
	case "wmoUnit:mm":
		return "mm"
	case "wmoUnit:m":
		return "m"
	case "wmoUnit:degree_(angle)":
		return "deg"
	default:
		return unit
	}
}

func display(req forecastRequest, weatherData map[string][]weatherPoint) {
	idx := map[string]int{}
	for _, p := range req.properties {
		idx[p] = 0
	}

	start := req.start.Truncate(time.Hour)
	end := req.end.Truncate(time.Hour)

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

		for _, property := range req.properties {
			// fmt.Println("DEBUG: PROP: ", property)
			points := weatherData[property]
			for idx[property] < len(points) {
				p := points[idx[property]]
				cmp := compareTimeToRange(curr, p.StartTime, p.EndTime)

				// fmt.Println("p: ", p)
				// fmt.Println("idx: ", idx[property])
				// fmt.Println("cmp: ", cmp)

				if cmp == 0 {
					row.values = append(row.values, formatWeatherValue(p, req.freedom))
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

	fmtstr, bar := getFormatString(req.properties)

	fmt.Printf(fmtstr, append([]interface{}{"time"}, toiface(req.properties)...)...)
	fmt.Println(bar)

	for _, r := range rows {
		fmt.Printf(fmtstr, append([]interface{}{r.at.In(req.displayTimeZone).Format(time.Stamp)}, toiface(r.values)...)...)
	}
}

func toiface(ss []string) []interface{} {
	is := make([]interface{}, len(ss))
	for i, v := range ss {
		is[i] = v
	}

	return is
}

func getFormatString(properties []string) (string, string) {
	fmtstr := " %15.15s"

	totwidth := 16

	for _, p := range properties {
		fmtstr += " | "

		width := len(p)
		if width < 15 {
			width = 15
		}

		fmtstr += fmt.Sprint("%", width, ".", width, "s")
		totwidth += 3 + width
	}

	fmtstr += "\n"

	return fmtstr, strings.Repeat("-", totwidth)
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
