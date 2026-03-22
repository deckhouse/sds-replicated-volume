/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package framework

import (
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
)

func newRetryTransport(maxRetries int) func(http.RoundTripper) http.RoundTripper {
	return func(rt http.RoundTripper) http.RoundTripper {
		return &retryTransport{next: rt, maxRetries: maxRetries}
	}
}

type retryTransport struct {
	next       http.RoundTripper
	maxRetries int
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := t.next.RoundTrip(req)
		if attempt >= t.maxRetries {
			return resp, err
		}
		if err == nil && resp.StatusCode < 500 {
			return resp, err
		}
		if resp != nil {
			resp.Body.Close()
		}
		wait := time.Duration(attempt+1) * 500 * time.Millisecond
		fmt.Fprintf(GinkgoWriter, "[retry] attempt %d/%d: %s %s -> %s, retrying in %s\n",
			attempt+1, t.maxRetries, req.Method, req.URL.Path, describeResult(resp, err), wait)
		select {
		case <-req.Context().Done():
			if err != nil {
				return nil, err
			}
			return resp, nil
		case <-time.After(wait):
		}
	}
}

func describeResult(resp *http.Response, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
}
