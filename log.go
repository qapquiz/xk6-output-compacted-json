package log

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"sort"

	"go.k6.io/k6/metrics"
	"go.k6.io/k6/output"
)

type Logger struct {
	out io.Writer
}

type Metric struct {
	RequestRate     float64 `json:"requestRate"`
	ErrorRate       float64 `json:"errorRate"`
	RequestDuration float64 `json:"requestDuration"`
}

type Summary struct {
	TotalRequest         int64   `json:"totalRequest"`
	TotalSuccess         int64   `json:"totalSuccess"`
	TotalError           int64   `json:"totalError"`
	RequestRatePerSecond float64 `json:"requestRatePerSecond"`
	RequestDuration      float64 `json:"requestDuration"`
}

type LoadTestResult struct {
	Points  map[int64]Metric `json:"points"`
	Summary Summary          `json:"summary"`
}

var loadTestResult LoadTestResult
var requestRate map[int64]float64
var errorRate map[int64]float64
var requestDuration map[int64][]float64
var flattenRequestDuration []float64

var filePath string
var file *os.File

func init() {
	output.RegisterExtension("compacted-json", New)
}

func New(params output.Params) (output.Output, error) {
  filePath = params.ConfigArgument
	return &Logger{params.StdOut}, nil
}

// Description implements output.Output.
func (*Logger) Description() string {
	return "This extension will return compacted json for k6 result"
}

// Start implements output.Output.
func (*Logger) Start() error {
	// initialize variable
	requestRate = make(map[int64]float64)
	errorRate = make(map[int64]float64)
	requestDuration = make(map[int64][]float64)
	flattenRequestDuration = make([]float64, 0)

	loadTestResult = LoadTestResult{
		Points: make(map[int64]Metric),
		Summary: Summary{
			TotalRequest:         0,
			TotalSuccess:         0,
			TotalError:           0,
			RequestRatePerSecond: 0,
			RequestDuration:      0,
		},
	}

	// create result file
	var err error
	file, err = os.Create(filePath)
	if err != nil {
		return err
	}

	return nil
}

// AddMetricSamples implements output.Output.
func (*Logger) AddMetricSamples(samples []metrics.SampleContainer) {
	for _, sample := range samples {
		all := sample.GetSamples()
		AggregateSamples(all)
	}
}

func AggregateSamples(samples []metrics.Sample) {
	unixTimestamp := samples[0].GetTime().Unix()
	for _, sample := range samples {
		if sample.Metric.Name == "http_reqs" {
			requestRate[unixTimestamp] += sample.Value
		}

		if sample.Metric.Name == "http_req_failed" {
			errorRate[unixTimestamp] += sample.Value
		}

		if sample.Metric.Name == "http_req_duration" {
			if requestDuration[unixTimestamp] == nil {
				requestDuration[unixTimestamp] = make([]float64, 0)
			}

			requestDuration[unixTimestamp] = append(requestDuration[unixTimestamp], sample.Value)
			flattenRequestDuration = append(flattenRequestDuration, sample.Value)
		}
	}
}

func Percentile(percentile float64, xs []float64) float64 {
	sort.Float64s(xs)
	kPercentMultiplyByN := percentile * float64(len(xs))
	index := int(math.Ceil(kPercentMultiplyByN))

	if math.Mod(kPercentMultiplyByN, 1) == 0 {
		return (xs[index] + xs[index+1]) / 2.0
	}

	return xs[index]
}

// Stop implements output.Output.
func (*Logger) Stop() error {
	flattenRequestRate := make([]float64, 0)
	totalRequest := int64(0)
	totalError := int64(0)
	countSeconds := 0

	for key := range requestRate {
		totalRequest += int64(requestRate[key])
		totalError += int64(errorRate[key])
		countSeconds += 1
		flattenRequestRate = append(flattenRequestRate, requestRate[key])

		loadTestResult.Points[key] = Metric{
			RequestRate:     requestRate[key],
			ErrorRate:       errorRate[key],
			RequestDuration: Percentile(0.95, requestDuration[key]),
		}
	}

	loadTestResult.Summary = Summary{
		TotalRequest:         totalRequest,
		TotalSuccess:         totalRequest - totalError,
		TotalError:           totalError,
		RequestRatePerSecond: float64(totalRequest) / float64(countSeconds-1),
		RequestDuration:      Percentile(0.95, flattenRequestDuration),
	}

	jsonData, err := json.MarshalIndent(loadTestResult, "", "    ")
	if err != nil {
		return err
	}

	defer file.Close()
	_, err = file.WriteString(string(jsonData))
	if err != nil {
		return err
	}

	return nil
}
