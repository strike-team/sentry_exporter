// Copyright 2019 Paolo Garri.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/log"
)

func extractErrorRate(reader io.Reader, config HTTPProbe) int {
	var stats [][]int
	count := 0
	body, err := ioutil.ReadAll(reader)
	if err != nil {
		return 0
	}
	err = json.Unmarshal([]byte(body), &stats)
	if err != nil {
		return 0
	}
	// ignore the last timestamp
	for i:=0; i < len(stats) - 1; i++ {
		count = count + stats[i][1]
	}
	return count
}

type RateLimitResponse struct {
	Window int
	Count int
}

type ProjectKeyResponse struct {
	ID string
	Name string
	Label string
	RateLimit *RateLimitResponse
}

func extractRateLimit(reader io.Reader, config HTTPProbe) float64 {
	var keys []ProjectKeyResponse
	body, err := ioutil.ReadAll(reader)
	if err != nil {
		return 0
	}
	err = json.Unmarshal([]byte(body), &keys)
	if err != nil || len(keys) == 0 || keys[0].RateLimit == nil {
		return 0
	}
	return float64(keys[0].RateLimit.Count) / float64(keys[0].RateLimit.Window)
}

func requestSentry(path string, config HTTPProbe, client *http.Client) (*http.Response, error) {
	requestURL := config.Prefix + path

	request, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		log.Errorf("Error creating request for target %s: %s", path, err)
		return &http.Response{}, err
	}

	for key, value := range config.Headers {
		if strings.Title(key) == "Host" {
			request.Host = value
			continue
		}
		request.Header.Set(key, value)
	}

	resp, err := client.Do(request)
	// Err won't be nil if redirects were turned off. See https://github.com/golang/go/issues/3795
	if err != nil {
		log.Warnf("Error for HTTP request to %s: %s", path, err)
	} else {
		if len(config.ValidStatusCodes) != 0 {
			for _, code := range config.ValidStatusCodes {
				if resp.StatusCode == code {
					return resp, nil
				}
			}
		} else if 200 <= resp.StatusCode && resp.StatusCode < 300 {
			return resp, nil
		}
	}
	return &http.Response{}, errors.New("Invalid response from Sentry API")
}

func requestEventCount(target string, stat string, config HTTPProbe, client *http.Client, w http.ResponseWriter) {
	// Get the last minute stats
	var lastMin = strconv.FormatInt(time.Now().Unix() - 60, 10)
	resp, err := requestSentry(target + "/stats/?resolution=10s&stat=" + stat + "&since=" + lastMin, config, client)

	if err == nil {
		defer resp.Body.Close()
		fmt.Fprintf(w, "sentry_events_total{stat=\"" + stat + "\"} %d\n", extractErrorRate(resp.Body, config))
	}
}

func requestRateLimit(target string, config HTTPProbe, client *http.Client, w http.ResponseWriter) {
	resp, err := requestSentry(target + "/keys/", config, client)

	if err == nil {
		defer resp.Body.Close()
		fmt.Fprintf(w, "sentry_rate_limit_seconds_total %f\n", extractRateLimit(resp.Body, config))
	}
}

func probeHTTP(target string, w http.ResponseWriter, module Module) (bool) {
	config := module.HTTP

	client := &http.Client{
		Timeout: module.Timeout,
	}

	requestEventCount(target, "received", config, client, w)
	requestEventCount(target, "rejected", config, client, w)
	requestRateLimit(target, config, client, w)

	return true
}
